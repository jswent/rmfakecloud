package inkpdf

import (
	"fmt"
	"image"
	"math"
	"strconv"

	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

const (
	xobjectPrefix = "RmInk"
	gsPrefix      = "RmGS"
)

// matrix is a PDF transformation matrix [a b c d e f].
type matrix [6]float64

// placement returns the cm operands that map the unit square onto the visual
// rectangle with bottom-left corner (X0, Y0) (visual PDF user space, y up)
// and size w x h, on a page with geometry g. With X0 = Y0 = 0 and w = h = 1 it
// is the matrix from visual PDF user space to the page's user space.
//
// Verified by rendering stamped quadrant images with poppler (pdftoppm) on
// pages with every /Rotate and with offset and cropped boxes; stamp_test.go
// checks these matrices and, when pdftoppm is installed, the rendered pixels:
//
//	rot   0: [ w  0  0  h  x0+X0  y0+Y0]
//	rot  90: [ 0  w -h  0  x1-Y0  y0+X0]
//	rot 180: [-w  0  0 -h  x1-X0  y1-Y0]
//	rot 270: [ 0 -w  h  0  x0+Y0  y1-X0]
func (g *pageGeom) placement(X0, Y0, w, h float64) matrix {
	switch g.rot {
	case 90:
		return matrix{0, w, -h, 0, g.x1 - Y0, g.y0 + X0}
	case 180:
		return matrix{-w, 0, 0, -h, g.x1 - X0, g.y1 - Y0}
	case 270:
		return matrix{0, -w, h, 0, g.x0 + Y0, g.y1 - X0}
	default:
		return matrix{w, 0, 0, h, g.x0 + X0, g.y0 + Y0}
	}
}

// imageMatrix maps the image unit square onto r (visual, top-left origin).
func (g *pageGeom) imageMatrix(r Rect) matrix {
	return g.placement(r.X, g.vh-r.Y-r.H, r.W, r.H)
}

// num formats a PDF real: 4 decimals, no exponent, no trailing zeros.
func num(v float64) string {
	v = math.Round(v*1e4) / 1e4
	if v == 0 {
		v = 0 // no "-0"
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}

func (m matrix) cm() string {
	return num(m[0]) + " " + num(m[1]) + " " + num(m[2]) + " " + num(m[3]) + " " + num(m[4]) + " " + num(m[5]) + " cm"
}

func validRect(r Rect) error {
	for _, v := range []float64{r.X, r.Y, r.W, r.H} {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return fmt.Errorf("inkpdf: invalid rect %+v", r)
		}
	}
	if r.W <= 0 || r.H <= 0 {
		return fmt.Errorf("inkpdf: invalid rect %+v: width and height must be positive", r)
	}
	return nil
}

// StampImage draws img over page pageNo, stretched onto r (visual page
// points, top-left origin). See StampPrepared.
func (d *Document) StampImage(pageNo int, img *image.NRGBA, r Rect, blend Blend) error {
	p, err := PrepareImage(img)
	if err != nil {
		return err
	}
	return d.StampPrepared(pageNo, p, r, blend)
}

// StampPrepared draws a prepared image over page pageNo, stretched onto r
// (visual page points, top-left origin; x and y scale independently).
//
// The page's existing content is wrapped in q … Q and one new content stream
// is appended, so the original graphics state cannot leak into the stamp.
// MediaBox, CropBox, /Rotate and /Annots are not modified. The page's
// Resources are copied before they are changed (they may be inherited from
// the page tree or shared with other pages), and the new XObject and
// ExtGState names never collide with existing ones.
func (d *Document) StampPrepared(pageNo int, p *Prepared, r Rect, blend Blend) error {
	if p == nil {
		return fmt.Errorf("inkpdf: nil prepared image")
	}
	if err := validRect(r); err != nil {
		return err
	}
	g, err := d.page(pageNo)
	if err != nil {
		return err
	}
	img, err := d.insertPrepared(p)
	if err != nil {
		return err
	}
	res := copyResources(g)
	xobj, err := d.copySubDict(res, "XObject")
	if err != nil {
		return err
	}
	name := uniqueName(xobj, xobjectPrefix)
	xobj[name] = *img

	ops := "\nQ\nq\n"
	if blend == BlendMultiply {
		gs, err := d.copySubDict(res, "ExtGState")
		if err != nil {
			return err
		}
		gsName := uniqueName(gs, gsPrefix)
		ref, err := d.multiplyGS()
		if err != nil {
			return err
		}
		gs[gsName] = *ref
		ops += "/" + gsName + " gs\n"
	}
	ops += g.imageMatrix(r).cm() + "\n/" + name + " Do\nQ\n"
	return d.appendContent(g, res, ops)
}

// StampContent draws a content stream over page pageNo. The stream is in
// visual PDF user space: points, origin at the bottom-left of the visible
// page, y up, size PageSize(pageNo); inkpdf applies the rotation and box
// offset. The stream and res are wrapped in a Form XObject with its own
// Resources, so names used by the caller cannot collide with the page's.
// The page is otherwise treated exactly as in StampPrepared.
func (d *Document) StampContent(pageNo int, content []byte, res Resources) error {
	g, err := d.page(pageNo)
	if err != nil {
		return err
	}
	formRes, err := res.toDict()
	if err != nil {
		return err
	}
	form, err := d.newStream(content, types.Dict{
		"Type":      types.Name("XObject"),
		"Subtype":   types.Name("Form"),
		"BBox":      types.NewNumberArray(0, 0, g.vw, g.vh),
		"Resources": formRes,
	})
	if err != nil {
		return err
	}
	pres := copyResources(g)
	xobj, err := d.copySubDict(pres, "XObject")
	if err != nil {
		return err
	}
	name := uniqueName(xobj, xobjectPrefix)
	xobj[name] = *form
	ops := "\nQ\nq\n" + g.placement(0, 0, 1, 1).cm() + "\n/" + name + " Do\nQ\n"
	return d.appendContent(g, pres, ops)
}

// copyResources returns a fresh, direct copy of the Resources dict in effect
// for the page (its own or inherited). Values are shared, the dict is not.
func copyResources(g *pageGeom) types.Dict {
	res := types.Dict{}
	for k, v := range g.attrs.Resources {
		res[k] = v
	}
	return res
}

// copySubDict replaces res[key] (direct or indirect, possibly shared) with a
// direct shallow copy and returns it.
func (d *Document) copySubDict(res types.Dict, key string) (types.Dict, error) {
	out := types.Dict{}
	if o, ok := res[key]; ok && o != nil {
		src, err := d.ctx.DereferenceDict(o)
		if err != nil {
			return nil, fmt.Errorf("inkpdf: resources /%s: %w", key, err)
		}
		for k, v := range src {
			out[k] = v
		}
	}
	res[key] = out
	return out, nil
}

func uniqueName(d types.Dict, prefix string) string {
	for i := 0; ; i++ {
		n := prefix + strconv.Itoa(i)
		if _, ok := d[n]; !ok {
			return n
		}
	}
}

// appendContent installs res as the page's own Resources and turns its
// Contents into [q, original..., ops].
func (d *Document) appendContent(g *pageGeom, res types.Dict, ops string) error {
	q, err := d.sharedQ()
	if err != nil {
		return err
	}
	post, err := d.newStream([]byte(ops), nil)
	if err != nil {
		return err
	}
	arr := types.Array{*q}
	if o, ok := g.dict["Contents"]; ok && o != nil {
		obj, err := d.ctx.Dereference(o)
		if err != nil {
			return fmt.Errorf("inkpdf: page contents: %w", err)
		}
		switch obj := obj.(type) {
		case types.Array:
			arr = append(arr, obj...)
		case nil:
			// Missing object: treat as no content.
		default:
			arr = append(arr, o) // a single stream: keep the indirect reference
		}
	}
	arr = append(arr, *post)
	g.dict.Update("Resources", res)
	g.dict.Update("Contents", arr)
	return nil
}

// sharedQ returns the one-byte "q" stream shared by every stamped page.
func (d *Document) sharedQ() (*types.IndirectRef, error) {
	if d.qRef == nil {
		ref, err := d.newStream([]byte("q\n"), nil)
		if err != nil {
			return nil, err
		}
		d.qRef = ref
	}
	return d.qRef, nil
}

func (d *Document) multiplyGS() (*types.IndirectRef, error) {
	if d.multiplyRef == nil {
		ref, err := d.ctx.IndRefForNewObject(types.Dict{
			"Type": types.Name("ExtGState"),
			"BM":   types.Name("Multiply"),
		})
		if err != nil {
			return nil, fmt.Errorf("inkpdf: extgstate: %w", err)
		}
		d.multiplyRef = ref
	}
	return d.multiplyRef, nil
}

// newStream adds a Flate-compressed stream object with the given extra dict
// entries and drops the uncompressed copy (the writer only uses Raw).
func (d *Document) newStream(content []byte, extra types.Dict) (*types.IndirectRef, error) {
	sd, err := d.ctx.NewStreamDictForBuf(content)
	if err != nil {
		return nil, fmt.Errorf("inkpdf: stream: %w", err)
	}
	for k, v := range extra {
		sd.Dict[k] = v
	}
	if err := sd.Encode(); err != nil {
		return nil, fmt.Errorf("inkpdf: stream: %w", err)
	}
	sd.Content = nil
	ref, err := d.ctx.IndRefForNewObject(*sd)
	if err != nil {
		return nil, fmt.Errorf("inkpdf: stream: %w", err)
	}
	return ref, nil
}
