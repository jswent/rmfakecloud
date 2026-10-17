package templates

import (
	"math"
	"strconv"
	"strings"
	"sync"

	"github.com/ddvk/rmfakecloud/internal/storage/exporter/inkpdf"
	log "github.com/sirupsen/logrus"
)

// DefaultPtPerUnit is the size of one page unit in PDF points, the factor
// of the tablet's own PDF export (it writes its page content under the
// matrix 0.317180616 0 0 -0.317180616 0 H, which is 72/227).
const DefaultPtPerUnit = 72.0 / 227.0

// Render returns a content stream that draws the template name on a page
// of pageW x pageH points (the visual page; pageH may be grown below the
// device page) at DefaultPtPerUnit. The stream is in visual PDF user space
// (bottom-left origin, y up) and needs no resources. ok is false for an
// unknown name or a template that draws nothing ("Blank"); the page is then
// left blank.
func Render(name string, pageW, pageH float64, landscape bool) (content []byte, res inkpdf.Resources, ok bool) {
	return RenderScaled(name, pageW, pageH, landscape, DefaultPtPerUnit)
}

// RenderScaled is Render with ppu points per page unit.
func RenderScaled(name string, pageW, pageH float64, landscape bool, ppu float64) (content []byte, res inkpdf.Resources, ok bool) {
	s, known := Lookup(name)
	if !known {
		warnUnknown(name)
		return nil, inkpdf.Resources{}, false
	}
	if s.Kind == Blank || ppu <= 0 || pageW <= 0 || pageH <= 0 {
		return nil, inkpdf.Resources{}, false
	}
	pg := pageFor(pageW/ppu, pageH/ppu, landscape)
	var b strings.Builder
	b.WriteString("q\n")
	// Page units, origin top-left, y down: the tablet's own matrix.
	b.WriteString(fmtNum(ppu, 9) + " 0 0 " + fmtNum(-ppu, 9) + " 0 " + num(pageH) + " cm\n")
	// Clip to the page: rules are drawn across the whole width with square
	// caps and must not leak into a neighbouring box.
	b.WriteString("0 0 " + num(pg.w) + " " + num(pg.h) + " re W n\n")
	draw(&b, s, pg)
	b.WriteString("Q\n")
	return []byte(b.String()), inkpdf.Resources{}, true
}

// page is the page a template is drawn on, in page units.
type page struct {
	w, h float64 // visual page (h includes growth)
	nomH float64 // height of the device page before growth
}

// mobile reports whether the page is a Paper Pro Move page. The device's
// template files switch to other offsets on pages narrower than 1000 units
// ("mobileMaxWidth").
func (p page) mobile() bool { return math.Min(p.w, p.nomH) < 1000 }

// landscape reports whether the page is wider than tall.
func (p page) landscape() bool { return p.w > p.nomH }

// originX is the left edge of the square canvas, nomH x nomH centred on the
// page, that the device's portrait templates are laid out on so that they
// also fit a landscape page ("templateWidth / 2 - templateHeight / 2" and
// "paperOriginX" in its template files): -234 on rM2, -270 on the Paper
// Pro, -319 on the Move, measured on "P Lined heading". templateHeight is
// the device page height: the rules of a grown page are as long as those
// of a page that was not grown.
func (p page) originX() float64 { return p.w/2 - p.nomH/2 }

// x resolves a position.
func (p page) x(pos Pos) float64 {
	if pos.Canvas {
		return p.originX() + pos.X
	}
	return pos.X
}

// devices are the portrait device pages in page units: rM1/rM2, Paper Pro,
// Paper Pro Move.
var devices = [][2]float64{{1404, 1872}, {1620, 2160}, {820, 1458}}

// pageFor finds the device page of a page w units wide, to know its height
// before growth. Unknown widths keep their height.
func pageFor(w, h float64, landscape bool) page {
	p := page{w: w, h: h, nomH: h}
	for _, d := range devices {
		dw, dh := d[0], d[1]
		if landscape || w > h {
			dw, dh = dh, dw
		}
		if math.Abs(w-dw) < 2 {
			p.nomH = dh
			return p
		}
	}
	return p
}

// draw appends the operators of s on pg.
func draw(b *strings.Builder, s Spec, pg page) {
	b.WriteString(num(RuleGrey) + " G " + num(RuleWidth) + " w 2 J 0 j\n")
	geometry(s, pg,
		func(x0, y0, x1, y1 float64) { line(b, x0, y0, x1, y1) },
		func(x, y float64) { b.WriteString(num(x) + " " + num(y) + " 1 1 re\n") })
	switch s.Kind {
	case Lines, Grid:
		b.WriteString("S\n")
	case Dots:
		// Filled black, stroked with the default rule style: what the
		// template file asks for (fillColor #000000, strokeWidth 1).
		b.WriteString("0 g 1 w B\n")
	}
}

// geometry enumerates the primitives of s on pg, in page units: rule
// segments (RuleWidth wide, square caps) and the 1x1 squares of a Dots
// template. Render strokes them; Paint rasterises them.
func geometry(s Spec, pg page, rule func(x0, y0, x1, y1 float64), dot func(x, y float64)) {
	switch s.Kind {
	case Lines:
		if s.Heading > 0 {
			x := s.HeadingX
			if pg.mobile() && s.HeadingXMobile > 0 {
				x = s.HeadingXMobile
			}
			rule(x, s.Heading, pg.w-x, s.Heading)
		}
		x0, x1 := 0.0, pg.w
		if s.Canvas {
			x0, x1 = math.Max(x0, pg.originX()), math.Min(x1, pg.originX()+pg.nomH)
		}
		first := s.FirstY
		if pg.mobile() && s.FirstYMobile != 0 {
			first = s.FirstYMobile
		}
		for _, y := range rules(first, s.Spacing, 0, pg.h) {
			rule(x0, y, x1, y)
		}
		if s.HasMargin {
			x := pg.x(s.Margin)
			rule(x, 0, x, pg.h)
		}
	case Grid:
		col := pg.x(s.column(pg))
		x0 := 0.0
		if s.RightOnly {
			x0 = col
		}
		for _, y := range rules(0, s.Spacing, 0, pg.h) {
			rule(x0, y, pg.w, y)
		}
		for _, x := range columns(col, s.Spacing, x0, s.RightOnly, pg.w) {
			rule(x, 0, x, pg.h)
		}
	case Dots:
		xs := columns(pg.x(s.column(pg)), s.Spacing, 0, false, pg.w)
		for _, y := range rules(0, s.Spacing, 0, pg.h) {
			for _, x := range xs {
				dot(x, y)
			}
		}
	}
}

// column returns the grid column for the page's orientation and device.
func (s Spec) column(pg page) Pos {
	i := 0
	if pg.landscape() {
		i = 1
	}
	if pg.mobile() {
		i += 2
	}
	return s.Columns[i]
}

// columns returns the x positions col + k*step inside [lo, w], k any
// integer (or k >= 0 with rightOnly).
func columns(col, step, lo float64, rightOnly bool, w float64) []float64 {
	if step <= 0 {
		return nil
	}
	first := col
	if !rightOnly {
		first = col - math.Ceil((col-lo)/step)*step
	}
	return rules(first, step, lo, w+1e-9)
}

// rules returns first, first+step, ... in [lo, hi). The positions are
// computed by multiplication, not accumulation, so the 77th rule of a grown
// page is where the tablet puts it.
func rules(first, step, lo, hi float64) []float64 {
	if step <= 0 {
		return nil
	}
	var ys []float64
	for i := 0; ; i++ {
		y := first + float64(i)*step
		if y >= hi {
			return ys
		}
		if y >= lo-1e-9 {
			ys = append(ys, y)
		}
	}
}

func line(b *strings.Builder, x0, y0, x1, y1 float64) {
	b.WriteString(num(x0) + " " + num(y0) + " m " + num(x1) + " " + num(y1) + " l\n")
}

// num formats v for a content stream: at most 4 decimals, no trailing zeros.
func num(v float64) string { return fmtNum(v, 4) }

// fmtNum formats v with at most prec decimals.
func fmtNum(v float64, prec int) string {
	s := strconv.FormatFloat(v, 'f', prec, 64)
	if strings.Contains(s, ".") {
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	if s == "-0" || s == "" {
		s = "0"
	}
	return s
}

var unknown sync.Map // template names already logged

func warnUnknown(name string) {
	if _, seen := unknown.LoadOrStore(name, true); !seen {
		log.Debugf("templates: unknown template %q; the page is left blank", name)
	}
}
