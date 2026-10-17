// Package inkpdf assembles exported PDFs on top of pdfcpu's model layer.
//
// It covers the operations the reMarkable exporter needs:
//   - notebooks: one image page per rendered page (AddImagePage, AddPreparedPage),
//   - annotated PDFs: transparent ink images stamped onto existing pages
//     (StampImage, StampPrepared) and vector content stamped onto pages
//     (StampContent), without rewriting the page boxes, /Rotate or annotations,
//   - inserted pages appended to a background document (AddBlankPage and the
//     image page functions), always as direct kids of the page tree root.
//
// pdfcpu's watermark API (api.AddWatermarksMap and friends) is deliberately not
// used: it rewrites the boxes of rotated pages, drops /Rotate and misplaces the
// background on pages whose MediaBox does not start at the origin.
//
// Coordinate systems (see the v6 rendering contract):
//   - Rect is in visual page points: the page as a viewer shows it after /Rotate,
//     origin at the top-left of the visible box (CropBox ∩ MediaBox), y down.
//   - StampContent takes a content stream in visual PDF user space: points,
//     origin at the bottom-left of the visible box, y up, page size = PageSize.
//
// /UserUnit is ignored (treated as 1). /Rotate values that are not multiples of
// 90 are treated as 0, consistently in PageSize and in the placement matrices.
//
// A Document is not safe for concurrent use. PrepareImage is: images can be
// encoded in parallel and then inserted sequentially with StampPrepared or
// AddPreparedPage.
package inkpdf

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

// ErrWrongPassword is pdfcpu's error for a missing or wrong user password.
// Open wraps it, so errors.Is(err, ErrWrongPassword) (or pdfcpu.ErrWrongPassword)
// identifies a background PDF that cannot be decrypted.
var ErrWrongPassword = pdfcpu.ErrWrongPassword

// Blend selects how a stamped image is composited with the page.
type Blend int

const (
	// BlendNormal is source-over compositing.
	BlendNormal Blend = iota
	// BlendMultiply darkens the page with the image (ExtGState /BM /Multiply),
	// keeping text under highlighter ink legible.
	BlendMultiply
)

// Rect is a placement in visual page points: origin at the top-left of the
// visible page as displayed (after /Rotate), y growing downwards.
type Rect struct{ X, Y, W, H float64 }

// Resources are the named resources a StampContent stream refers to.
type Resources struct {
	// ExtGState maps a resource name (without the leading slash) to the
	// entries of an ExtGState dictionary, e.g. {"CA": 0.6, "ca": 0.6, "BM": "Multiply"}.
	// Go strings become PDF names; numbers, bools, []any and map[string]any
	// are converted to the matching PDF objects.
	ExtGState map[string]map[string]any
}

// OpenOptions control how an existing PDF is opened.
type OpenOptions struct {
	UserPassword, OwnerPassword string
	// Decrypt writes the output without encryption. Without it, an encrypted
	// input is re-encrypted on Write. It has no effect on a plain PDF.
	Decrypt bool
}

// Document is a PDF being assembled. It wraps a pdfcpu *model.Context.
type Document struct {
	ctx *model.Context

	qRef        *types.IndirectRef // shared "q" stream prepended to stamped pages
	multiplyRef *types.IndirectRef // shared ExtGState {/BM /Multiply}
}

// newConfig returns a pdfcpu configuration that never reads or writes the
// user's config directory. A nil configuration must never reach pdfcpu: it
// would create $UserConfigDir/pdfcpu on every call.
func newConfig() *model.Configuration {
	conf := model.NewStatelessConfiguration()
	conf.ValidationMode = model.ValidationRelaxed
	return conf
}

// NewDocument returns an empty document (no pages).
func NewDocument() (*Document, error) {
	// The page size only sets the root's inheritable MediaBox; every page we
	// add carries its own MediaBox, CropBox and Rotate.
	ctx, err := pdfcpu.CreateContextWithXRefTable(newConfig(), types.PaperSize["A4"])
	if err != nil {
		return nil, fmt.Errorf("inkpdf: new document: %w", err)
	}
	return &Document{ctx: ctx}, nil
}

// Open reads and validates (relaxed) a background PDF.
//
// For an encrypted PDF whose user password is missing or wrong, the returned
// error wraps pdfcpu.ErrWrongPassword (also exported as ErrWrongPassword).
func Open(rs io.ReadSeeker, opts OpenOptions) (*Document, error) {
	conf := newConfig()
	conf.UserPW = opts.UserPassword
	conf.OwnerPW = opts.OwnerPassword
	if opts.Decrypt {
		conf.Cmd = model.DECRYPT // the writer then drops /Encrypt
	}
	ctx, err := api.ReadAndValidate(context.Background(), rs, conf)
	if opts.Decrypt && errors.Is(err, pdfcpu.ErrNotEncrypted) {
		// Decrypt is a request, not an assertion: a plain PDF is read as is.
		if _, serr := rs.Seek(0, io.SeekStart); serr != nil {
			return nil, fmt.Errorf("inkpdf: open: %w", serr)
		}
		conf.Cmd = model.VALIDATE
		ctx, err = api.ReadAndValidate(context.Background(), rs, conf)
	}
	if err != nil {
		if errors.Is(err, pdfcpu.ErrWrongPassword) {
			return nil, fmt.Errorf("inkpdf: open encrypted pdf: %w", err)
		}
		return nil, fmt.Errorf("inkpdf: open: %w", err)
	}
	return &Document{ctx: ctx}, nil
}

// PageCount returns the number of pages.
func (d *Document) PageCount() int { return d.ctx.PageCount }

// PageSize returns the visual size in points of page pageNo (1-based): the
// CropBox intersected with the MediaBox, with width and height swapped for
// /Rotate 90 and 270.
func (d *Document) PageSize(pageNo int) (w, h float64, err error) {
	g, err := d.page(pageNo)
	if err != nil {
		return 0, 0, err
	}
	return g.vw, g.vh, nil
}

// Write serialises the document. A Document should be written once.
func (d *Document) Write(w io.Writer) error {
	if err := api.WriteContext(context.Background(), d.ctx, w); err != nil {
		return fmt.Errorf("inkpdf: write: %w", err)
	}
	return nil
}

// pageGeom is a page dict with the geometry in effect for it.
type pageGeom struct {
	dict           types.Dict
	attrs          *model.InheritedPageAttrs
	x0, y0, x1, y1 float64 // visible box (CropBox ∩ MediaBox) in user space
	rot            int     // 0, 90, 180 or 270
	vw, vh         float64 // visual size
}

func (d *Document) page(pageNo int) (*pageGeom, error) {
	if pageNo < 1 || pageNo > d.ctx.PageCount {
		return nil, fmt.Errorf("inkpdf: page %d out of range (1..%d)", pageNo, d.ctx.PageCount)
	}
	dict, _, attrs, err := d.ctx.PageDict(context.Background(), pageNo, false)
	if err != nil {
		return nil, fmt.Errorf("inkpdf: page %d: %w", pageNo, err)
	}
	if dict == nil || attrs == nil {
		return nil, fmt.Errorf("inkpdf: page %d: not found", pageNo)
	}
	mb := attrs.MediaBox
	if mb == nil {
		return nil, fmt.Errorf("inkpdf: page %d: no MediaBox", pageNo)
	}
	// Normalise rectangles given as [urx ury llx lly].
	x0, x1 := min(mb.LL.X, mb.UR.X), max(mb.LL.X, mb.UR.X)
	y0, y1 := min(mb.LL.Y, mb.UR.Y), max(mb.LL.Y, mb.UR.Y)
	if cb := attrs.CropBox; cb != nil {
		cx0, cx1 := max(x0, min(cb.LL.X, cb.UR.X)), min(x1, max(cb.LL.X, cb.UR.X))
		cy0, cy1 := max(y0, min(cb.LL.Y, cb.UR.Y)), min(y1, max(cb.LL.Y, cb.UR.Y))
		// An empty intersection is invalid; fall back to the MediaBox.
		if cx1 > cx0 && cy1 > cy0 {
			x0, y0, x1, y1 = cx0, cy0, cx1, cy1
		}
	}
	if x1 <= x0 || y1 <= y0 {
		return nil, fmt.Errorf("inkpdf: page %d: empty page box", pageNo)
	}
	rot := ((attrs.Rotate % 360) + 360) % 360
	if rot%90 != 0 {
		rot = 0
	}
	g := &pageGeom{dict: dict, attrs: attrs, x0: x0, y0: y0, x1: x1, y1: y1, rot: rot, vw: x1 - x0, vh: y1 - y0}
	if rot == 90 || rot == 270 {
		g.vw, g.vh = g.vh, g.vw
	}
	return g, nil
}
