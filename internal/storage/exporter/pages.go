package exporter

import (
	"image"
	"math"

	"github.com/ddvk/rmfakecloud/internal/storage/exporter/inkpdf"
	"github.com/ddvk/rmfakecloud/internal/storage/exporter/rmlines"
)

// Page geometry.
//
// Page units are the tablet's page coordinates as stored in the .rm file and
// reported by rmlines (rM1/rM2 1404x1872, Paper Pro 1620x2160, Paper Pro Move
// 820x1458). One factor converts all of them to the size of the tablet's own
// PDF export: 72/227 pt per unit gives 445.3x593.7 (rM2; the tablet writes
// 445x594), 513.8x685.1 (Paper Pro; 514x685) and 260.1x462.5 (Move; 260x463).
// Measured on the user's device exports, 2026-10-17. Page sizes are rounded
// to 0.1 pt.

// DefaultPtPerUnit is the default size of one page unit in PDF points.
const DefaultPtPerUnit = 72.0 / 227.0

// Default device page in page units (rM1/rM2), used for v3/v5 pages and for
// blank pages before any v6 page told us the device's size.
const (
	deviceW = 1404
	deviceH = 1872
)

// tabletPad is the padding below the lowest ink the tablet adds when a page
// grows: its PDF page height is (lowest ink y + 48 units) * pt per unit on
// every grown page measured (9 pages, three devices).
const tabletPad = 48

// maxGrowth bounds how far a page may grow, as a multiple of the page size.
// The Move sample has a page 4.6 pages tall; rmlines' own default (4) would
// cut it.
const maxGrowth = 12

// roundPt rounds to 0.1 pt.
func roundPt(v float64) float64 { return math.Round(v*10) / 10 }

// geometry converts page units to points.
type geometry struct {
	ppu   float64 // points per page unit
	scale float64 // image pixels per page unit
	clip  bool
}

// pt converts a length in page units to points, rounded to 0.1 pt.
func (g geometry) pt(units float64) float64 { return roundPt(units * g.ppu) }

// nominalSize is the size in points of a page of w x h page units.
func (g geometry) nominalSize(w, h int, landscape bool) (float64, float64) {
	if landscape && w < h {
		w, h = h, w
	}
	return g.pt(float64(w)), g.pt(float64(h))
}

// notebookFrame is the part of a rendered page that becomes a notebook page,
// in page units. The tablet keeps the page width and grows the page
// downwards only, so with clipping the frame is [0, PageW] x [0, max(PageH,
// Extent.B)]: ink left of, right of or above the page is cut off. Without
// clipping (RMLINES_CLIP=false, for debugging) the frame is the renderer's
// whole extent.
func (g geometry) notebookFrame(r *rmlines.Result) rmlines.Extent {
	if !g.clip {
		return r.Extent
	}
	return rmlines.Extent{L: 0, T: 0, R: float64(r.PageW), B: math.Max(float64(r.PageH), r.Extent.B)}
}

// cropRect is the pixel rectangle of an image covering ext (at g.scale) that
// shows frame, clamped to the image.
func (g geometry) cropRect(img image.Rectangle, ext, frame rmlines.Extent) image.Rectangle {
	px := func(v float64) int { return int(math.Round(v * g.scale)) }
	r := image.Rect(
		img.Min.X+px(frame.L-ext.L), img.Min.Y+px(frame.T-ext.T),
		img.Min.X+px(frame.R-ext.L), img.Min.Y+px(frame.B-ext.T),
	)
	return r.Intersect(img)
}

// crop returns the part of img inside r without copying pixels.
func crop(img *image.NRGBA, r image.Rectangle) *image.NRGBA {
	if img == nil || r == img.Rect {
		return img
	}
	return img.SubImage(r).(*image.NRGBA)
}

// v6Placement is where the image of a rendered v6 page goes on a background
// PDF page that is vw points wide, in visual page points (inkpdf.Rect: origin
// top-left, y down).
//
// v6 ink on a PDF is drawn at a fixed scale, not fitted to the PDF page
// (measured by giovi321 on a 960x540 slide deck annotated on an rM2): device
// x = 0 is the horizontal centre of the PDF page and y = 0 its top edge, so
// x_pt = vw/2 + x_dev*ppu and y_pt = y_dev*ppu. rmlines reports the extent in
// its frame, where the page's left edge is 0 (device x = -pageW/2), hence
// the pageW/2 shift. In landscape documents rmlines already swapped the
// frame, so pageW is the landscape width.
//
// TODO(validation): measured on rM2 only, with 72/226 pt per unit; it still
// needs checking against tablet exports of annotated PDFs from the Paper Pro
// and the Move.
func v6Placement(ext rmlines.Extent, pageW int, vw, ppu float64) inkpdf.Rect {
	return inkpdf.Rect{
		X: vw/2 + (ext.L-float64(pageW)/2)*ppu,
		Y: ext.T * ppu,
		W: (ext.R - ext.L) * ppu,
		H: (ext.B - ext.T) * ppu,
	}
}
