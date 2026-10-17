package exporter

import (
	"image"

	"github.com/ddvk/rmfakecloud/internal/storage/exporter/templates"
)

// Notebook pages are exported as one opaque image: the page template, the
// ink and the highlights composited on white by the server, instead of a
// transparent ink image (8-bit grey or RGB plus an 8-bit soft mask) over a
// vector template. Viewers decode an opaque image far more cheaply: pdf.js
// in the web interface has to inflate the mask, merge it with the colour
// and un-premultiply every pixel of a transparent image, and a page that
// was grown on the tablet to 1620 x 6328 px took seconds to appear while
// the same page flattened takes well under one. Ink stamped onto the pages
// of an annotated PDF stays transparent: there the backdrop is not ours.

// flatten composites, into ink's own pixels, the page background bg (white
// with the template), then ink over it (straight alpha, source-over), then
// hl multiplied over that (the highlighter blend used in the PDF, so the
// result equals the layered page). bg must have ink's bounds; hl may be
// nil, otherwise it has ink's bounds too. Every pixel ends opaque.
func flatten(ink, hl *image.NRGBA, bg *image.Gray) {
	b := ink.Rect
	w := b.Dx()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		o := ink.PixOffset(b.Min.X, y)
		irow := ink.Pix[o : o+w*4]
		o = bg.PixOffset(b.Min.X, y)
		brow := bg.Pix[o : o+w]
		var hrow []byte
		if hl != nil {
			o = hl.PixOffset(b.Min.X, y)
			hrow = hl.Pix[o : o+w*4]
		}
		for x, i := 0, 0; x < w; x, i = x+1, i+4 {
			px := irow[i : i+4 : i+4]
			switch a := uint32(px[3]); a {
			case 255:
			case 0:
				px[0], px[1], px[2] = brow[x], brow[x], brow[x]
			default:
				g := uint32(brow[x]) * (255 - a)
				px[0] = uint8((uint32(px[0])*a + g + 127) / 255)
				px[1] = uint8((uint32(px[1])*a + g + 127) / 255)
				px[2] = uint8((uint32(px[2])*a + g + 127) / 255)
			}
			px[3] = 255
			if hrow == nil {
				continue
			}
			hp := hrow[i : i+4 : i+4]
			if ha := uint32(hp[3]); ha != 0 {
				// Multiply with coverage ha: c * (1 - ha*(1-hc)).
				for k := 0; k < 3; k++ {
					f := 255*255 - ha*(255-uint32(hp[k]))
					px[k] = uint8((uint32(px[k])*f + 255*255/2) / (255 * 255))
				}
			}
		}
	}
}

// pageBackground returns the opaque background of a notebook page whose
// image has bounds r and covers the page frame (units) at scale pixels per
// unit: white, with the page template when one is drawn.
func (e *Exporter) pageBackground(j *job, r image.Rectangle, landscape bool) *image.Gray {
	bg := image.NewGray(r)
	for i := range bg.Pix {
		bg.Pix[i] = 255
	}
	if !e.cfg.NoTemplates && j.page.Template != "" {
		templates.Paint(j.page.Template, bg, j.frame.R-j.frame.L, j.frame.B-j.frame.T, landscape, e.cfg.Scale, 0, 0)
	}
	return bg
}
