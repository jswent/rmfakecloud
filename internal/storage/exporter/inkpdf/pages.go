package inkpdf

import (
	"fmt"
	"image"
	"math"

	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

// Page sizes outside this range are rejected (PDF 1.7 implementation limits).
const (
	minPagePt = 3.0
	maxPagePt = 14400.0
)

func validPageSize(w, h float64) error {
	for _, v := range []float64{w, h} {
		if math.IsNaN(v) || v < minPagePt || v > maxPagePt {
			return fmt.Errorf("inkpdf: invalid page size %vx%v pt (allowed %v..%v)", w, h, minPagePt, maxPagePt)
		}
	}
	return nil
}

// AddImagePage appends a widthPt x heightPt page that img fills (stretched
// if the aspect ratios differ) and returns its 1-based page number.
func (d *Document) AddImagePage(img *image.NRGBA, widthPt, heightPt float64) (int, error) {
	if err := validPageSize(widthPt, heightPt); err != nil {
		return 0, err
	}
	p, err := PrepareImage(img)
	if err != nil {
		return 0, err
	}
	return d.AddPreparedPage(p, widthPt, heightPt)
}

// AddPreparedPage is AddImagePage for an image prepared with PrepareImage.
func (d *Document) AddPreparedPage(p *Prepared, widthPt, heightPt float64) (int, error) {
	if p == nil {
		return 0, fmt.Errorf("inkpdf: nil prepared image")
	}
	if err := validPageSize(widthPt, heightPt); err != nil {
		return 0, err
	}
	img, err := d.insertPrepared(p)
	if err != nil {
		return 0, err
	}
	name := xobjectPrefix + "0"
	content, err := d.newStream([]byte("q\n"+matrix{widthPt, 0, 0, heightPt, 0, 0}.cm()+"\n/"+name+" Do\nQ\n"), nil)
	if err != nil {
		return 0, err
	}
	return d.appendPage(widthPt, heightPt, types.Dict{"XObject": types.Dict{name: *img}}, content)
}

// AddBlankPage appends an empty widthPt x heightPt page and returns its
// 1-based page number. Content can be added with StampImage/StampContent.
func (d *Document) AddBlankPage(widthPt, heightPt float64) (int, error) {
	if err := validPageSize(widthPt, heightPt); err != nil {
		return 0, err
	}
	return d.appendPage(widthPt, heightPt, types.Dict{}, nil)
}

// appendPage adds a page as the last kid of the page tree root, so appended
// pages are always at depth 1 regardless of how many are added. MediaBox,
// CropBox, Rotate and Resources are set on the page so nothing is inherited
// from the root (a background PDF's root may carry any of them).
func (d *Document) appendPage(w, h float64, res types.Dict, content *types.IndirectRef) (int, error) {
	rootRef, err := d.ctx.Pages()
	if err != nil {
		return 0, fmt.Errorf("inkpdf: page tree: %w", err)
	}
	root, err := d.ctx.DereferenceDict(*rootRef)
	if err != nil || root == nil {
		return 0, fmt.Errorf("inkpdf: page tree root: %v", err)
	}
	box := types.NewNumberArray(0, 0, w, h)
	page := types.Dict{
		"Type":      types.Name("Page"),
		"Parent":    *rootRef,
		"MediaBox":  box,
		"CropBox":   box.Clone(),
		"Rotate":    types.Integer(0),
		"Resources": res,
	}
	if content != nil {
		page["Contents"] = *content
	}
	ref, err := d.ctx.IndRefForNewObject(page)
	if err != nil {
		return 0, fmt.Errorf("inkpdf: page: %w", err)
	}

	// Kids and Count may be indirect in a background PDF; resolve them
	// instead of relying on direct entries.
	var kids types.Array
	if o, ok := root["Kids"]; ok && o != nil {
		a, err := d.ctx.DereferenceArray(o)
		if err != nil {
			return 0, fmt.Errorf("inkpdf: page tree kids: %w", err)
		}
		kids = append(kids, a...)
	}
	kids = append(kids, *ref)
	root["Kids"] = kids

	count := 0
	if o, ok := root["Count"]; ok && o != nil {
		n, err := d.ctx.DereferenceInteger(o)
		if err != nil {
			return 0, fmt.Errorf("inkpdf: page tree count: %w", err)
		}
		if n != nil {
			count = n.Value()
		}
	}
	root["Count"] = types.Integer(count + 1)
	d.ctx.PageCount++
	return d.ctx.PageCount, nil
}
