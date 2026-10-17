package templates

import (
	"image"
	"math"
)

// Paint draws the template name onto bg, an 8-bit grey image of a notebook
// page, as a viewer shows the vector page Render makes at the same
// resolution: rules RuleWidth wide in RuleGrey with square caps, dots as
// 1x1 squares stroked 1 unit wide, everything clipped to the page. Edges
// are anti-aliased by area coverage, except that a rule thinner than a
// pixel is drawn one full pixel wide, on the pixel row (or column) that
// holds its centre: viewers never draw a line thinner than a device pixel
// (poppler and pdf.js both widen hairlines to one pixel), so the 0.5-unit
// rules of a page at one pixel per unit come out as crisp one-pixel lines,
// and so do they here.
//
// The page is pageW x pageH page units (pageH includes growth); pixel
// (i, j) of bg, counted from bg.Rect.Min, covers the units
// [ox + i/scale, ox + (i+1)/scale) x [oy + j/scale, oy + (j+1)/scale).
// Paint reports whether the template is known and draws anything; it never
// touches bg otherwise.
func Paint(name string, bg *image.Gray, pageW, pageH float64, landscape bool, scale, ox, oy float64) bool {
	s, known := Lookup(name)
	if !known {
		warnUnknown(name)
		return false
	}
	if s.Kind == Blank || bg == nil || scale <= 0 || pageW <= 0 || pageH <= 0 {
		return false
	}
	pg := pageFor(pageW, pageH, landscape)
	r := raster{bg: bg, scale: scale, ox: ox, oy: oy, clip: [4]float64{0, 0, pg.w, pg.h}}
	geometry(s, pg,
		func(x0, y0, x1, y1 float64) {
			// A stroked segment with square caps is a rectangle grown by
			// half the rule width on every side.
			const hw = RuleWidth / 2
			r.fill(math.Min(x0, x1)-hw, math.Min(y0, y1)-hw, math.Max(x0, x1)+hw, math.Max(y0, y1)+hw)
		},
		func(x, y float64) {
			// "x y 1 1 re" filled black, then stroked 1 unit wide in grey:
			// the stroke covers the whole square, so the dot is a 2x2 grey
			// square.
			r.fill(x-0.5, y-0.5, x+1.5, y+1.5)
		})
	return true
}

// raster fills rectangles in page units on a grey image.
type raster struct {
	bg     *image.Gray
	scale  float64
	ox, oy float64
	clip   [4]float64 // page rectangle, units
}

// ruleValue is RuleGrey as an 8-bit level (192).
var ruleValue = math.Round(RuleGrey * 255)

// fill paints the rectangle [x0, x1] x [y0, y1] (units) in RuleGrey,
// clipped to the page, blending partly covered pixels by their coverage.
func (r *raster) fill(x0, y0, x1, y1 float64) {
	x0, y0 = math.Max(x0, r.clip[0]), math.Max(y0, r.clip[1])
	x1, y1 = math.Min(x1, r.clip[2]), math.Min(y1, r.clip[3])
	if x1 <= x0 || y1 <= y0 {
		return
	}
	// To pixel coordinates relative to the image origin.
	px0, px1 := onePixel((x0-r.ox)*r.scale, (x1-r.ox)*r.scale)
	py0, py1 := onePixel((y0-r.oy)*r.scale, (y1-r.oy)*r.scale)
	b := r.bg.Rect
	w, h := b.Dx(), b.Dy()
	i0, i1 := int(math.Floor(px0)), int(math.Ceil(px1))
	j0, j1 := int(math.Floor(py0)), int(math.Ceil(py1))
	i0, j0 = max(i0, 0), max(j0, 0)
	i1, j1 = min(i1, w), min(j1, h)
	if i1 <= i0 || j1 <= j0 {
		return
	}
	// Coverage along each axis: 1 inside, partial on the two edge pixels.
	cx := make([]float64, i1-i0)
	for i := range cx {
		cx[i] = math.Min(px1, float64(i0+i+1)) - math.Max(px0, float64(i0+i))
	}
	for j := j0; j < j1; j++ {
		cy := math.Min(py1, float64(j+1)) - math.Max(py0, float64(j))
		o := r.bg.PixOffset(b.Min.X+i0, b.Min.Y+j)
		row := r.bg.Pix[o : o+i1-i0]
		for i, v := range row {
			c := cx[i] * cy
			if c <= 0 {
				continue
			}
			// Blend towards the rule grey by coverage; a pixel already
			// darker than the rule (a crossing) is not lightened.
			nv := float64(v) - (float64(v)-ruleValue)*c
			if nv < float64(v) {
				row[i] = uint8(math.Round(nv))
			}
		}
	}
}

// onePixel widens the span [a, b] (pixels) to the whole pixel that holds
// its centre when it is thinner than a pixel.
func onePixel(a, b float64) (float64, float64) {
	if b-a >= 1 {
		return a, b
	}
	p := math.Floor((a + b) / 2)
	return p, p + 1
}
