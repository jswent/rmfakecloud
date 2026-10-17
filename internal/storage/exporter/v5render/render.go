package v5render

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/juruen/rmapi/encoding/rm"
)

// Device screen size in pixels (rM1/rM2), the frame v3/v5 ink is stored in.
const (
	DeviceW = 1404
	DeviceH = 1872
)

// PointsPerPixel is the rM1/rM2 screen resolution, 72/226 pt per pixel.
const PointsPerPixel = 72.0 / 226.0

// DefaultPageW and DefaultPageH are the size in points of a notebook page
// with no background: 1404 x 1872 px at 72/226 pt per pixel
// (447.29 x 596.39 pt).
const (
	DefaultPageW = DeviceW * PointsPerPixel
	DefaultPageH = DeviceH * PointsPerPixel
)

// HighlightGState is the name the content stream uses for the highlighter's
// graphics state. It is a key of Output.Resources.ExtGState.
const HighlightGState = "RmGSv5Highlight"

// ColorMode selects colour or greyscale output.
type ColorMode int

const (
	ColorFull ColorMode = iota // default
	ColorGray                  // every colour turned to its luminance
)

// Options describe the page the ink is drawn on.
type Options struct {
	PageW, PageH float64 // visual page size in points
	Landscape    bool    // document orientation from .content
	ColorMode    ColorMode
}

// Resources are the resources a content stream refers to.
//
// It mirrors inkpdf.Resources (Track C), which does not exist on this branch
// yet; integration replaces it with that type.
type Resources struct {
	ExtGState map[string]map[string]any // name -> PDF dict entries
}

// Output is a content stream in visual PDF user space and its resources.
type Output struct {
	Content   []byte
	Resources Resources
}

// Pen codes rmapi does not name.
const (
	calligraphy rm.BrushType = 21
)

type penKind int

const (
	penSkip     penKind = iota
	penConstant         // fineliner, ballpoint, marker, unknown
	penSharp            // mechanical pencil
	penPressure         // paintbrush, calligraphy
	penTilt             // tilt pencil
	penHighlight
)

func kindOf(t rm.BrushType) penKind {
	switch t {
	case rm.Eraser, rm.EraseArea, 19: // 19: v5 eraser (rmrl PEN_MAPPING)
		return penSkip
	case rm.Highlighter, rm.HighlighterV5:
		return penHighlight
	case rm.Brush, rm.BrushV5, calligraphy:
		return penPressure
	case rm.TiltPencil, rm.TiltPencilV5:
		return penTilt
	case rm.SharpPencil, rm.SharpPencilV5:
		return penSharp
	default:
		return penConstant
	}
}

type rgb struct{ r, g, b float64 }

func rgb8(r, g, b int) rgb { return rgb{float64(r) / 255, float64(g) / 255, float64(b) / 255} }

// palette is rmc's RM_PALETTE, indexed by the colour code in the file.
var palette = map[rm.BrushColor]rgb{
	0: rgb8(0, 0, 0),       // black
	1: rgb8(144, 144, 144), // grey
	2: rgb8(255, 255, 255), // white
	3: rgb8(251, 247, 25),  // yellow
	4: rgb8(0, 255, 0),     // green
	5: rgb8(255, 192, 203), // pink
	6: rgb8(78, 105, 201),  // blue
	7: rgb8(179, 62, 57),   // red
	8: rgb8(125, 125, 125), // grey overlap
}

const highlightYellow rm.BrushColor = 3

func colorOf(line *rm.Line, kind penKind) rgb {
	code := line.BrushColor
	if kind == penHighlight && code <= rm.White {
		code = highlightYellow
	}
	c, ok := palette[code]
	if !ok {
		c = palette[rm.Black]
	}
	return c
}

// lighten moves c towards white; strength 1 keeps c, 0 gives white.
func lighten(c rgb, strength float64) rgb {
	f := func(v float64) float64 { return 1 - (1-v)*strength }
	return rgb{f(c.r), f(c.g), f(c.b)}
}

func gray(c rgb) rgb {
	y := 0.299*c.r + 0.587*c.g + 0.114*c.b
	return rgb{y, y, y}
}

// transform maps device pixels to visual PDF user space.
type transform struct {
	scale  float64
	pageH  float64
	turned bool
}

func (t transform) apply(p rm.Point) (x, y float64) {
	if t.turned {
		return (DeviceH - float64(p.Y)) * t.scale, t.pageH - float64(p.X)*t.scale
	}
	return float64(p.X) * t.scale, t.pageH - float64(p.Y)*t.scale
}

// transformFor ports pdf.go's transformFor (commit 430ac58), adding the
// Landscape flag as a second reason to turn the ink.
func transformFor(opts Options) transform {
	if opts.Landscape || opts.PageW > opts.PageH {
		return transform{scale: opts.PageW / DeviceH, pageH: opts.PageH, turned: true}
	}
	scale := opts.PageH / DeviceH
	if opts.PageH/opts.PageW < 1.33 {
		scale = opts.PageW / DeviceW
	}
	return transform{scale: scale, pageH: opts.PageH}
}

const (
	minWidthPt         = 0.1 // floor so no stroke vanishes
	defaultHighlightPx = 30  // pdf.go's highlighter width
	sharpStrength      = 0.7
)

func finite(v float32) bool {
	f := float64(v)
	return !math.IsNaN(f) && !math.IsInf(f, 0)
}

func pointsFinite(pts []rm.Point) bool {
	for _, p := range pts {
		if !finite(p.X) || !finite(p.Y) || !finite(p.Width) || !finite(p.Pressure) {
			return false
		}
	}
	return true
}

func meanWidth(pts []rm.Point) float64 {
	sum := 0.0
	for _, p := range pts {
		sum += float64(p.Width)
	}
	return sum / float64(len(pts))
}

func clamp01(v float64) float64 { return math.Max(0, math.Min(1, v)) }

// Render draws page's ink for a page of the size in opts.
func Render(page *rm.Rm, opts Options) (*Output, error) {
	if page == nil {
		return nil, errors.New("v5render: nil page")
	}
	for _, v := range []float64{opts.PageW, opts.PageH} {
		if !(v > 0) || math.IsInf(v, 0) {
			return nil, fmt.Errorf("v5render: invalid page size %gx%g", opts.PageW, opts.PageH)
		}
	}

	w := &writer{t: transformFor(opts), gray: opts.ColorMode == ColorGray}
	w.op("q")
	w.op("1 J 1 j")
	for li := range page.Layers {
		for i := range page.Layers[li].Lines {
			w.line(&page.Layers[li].Lines[i])
		}
	}
	w.op("Q")

	out := &Output{Content: []byte(w.b.String())}
	if w.usedHighlight {
		out.Resources.ExtGState = map[string]map[string]any{
			HighlightGState: {"Type": "ExtGState", "CA": 0.5, "BM": "Multiply"},
		}
	}
	return out, nil
}

type writer struct {
	b             strings.Builder
	t             transform
	gray          bool
	usedHighlight bool
}

func (w *writer) op(s string) {
	w.b.WriteString(s)
	w.b.WriteByte('\n')
}

// num formats v with at most 3 decimals and no trailing zeros.
func num(v float64) string {
	s := strconv.FormatFloat(v, 'f', 3, 64)
	s = strings.TrimRight(s, "0")
	s = strings.TrimSuffix(s, ".")
	if s == "-0" || s == "" {
		s = "0"
	}
	return s
}

func (w *writer) color(c rgb) string {
	if w.gray {
		c = gray(c)
	}
	return num(c.r) + " " + num(c.g) + " " + num(c.b) + " RG"
}

func (w *writer) width(px float64) string {
	return num(math.Max(px*w.t.scale, minWidthPt)) + " w"
}

func (w *writer) path(pts []rm.Point) {
	for i, p := range pts {
		x, y := w.t.apply(p)
		if i == 0 {
			w.op(num(x) + " " + num(y) + " m")
		} else {
			w.op(num(x) + " " + num(y) + " l")
		}
	}
	if len(pts) == 1 { // a dot: zero-length segment, drawn by the round cap
		x, y := w.t.apply(pts[0])
		w.op(num(x) + " " + num(y) + " l")
	}
	w.op("S")
}

func (w *writer) line(line *rm.Line) {
	kind := kindOf(line.BrushType)
	if kind == penSkip || len(line.Points) == 0 || !pointsFinite(line.Points) {
		return
	}
	base := colorOf(line, kind)

	switch kind {
	case penHighlight:
		px := meanWidth(line.Points)
		if px <= 0 {
			px = defaultHighlightPx
		}
		w.usedHighlight = true
		w.op("q")
		w.op("/" + HighlightGState + " gs")
		w.op("2 J")
		w.op(w.width(px))
		w.op(w.color(base))
		w.path(line.Points)
		w.op("Q")
	case penConstant, penSharp:
		c := base
		if kind == penSharp {
			c = lighten(c, sharpStrength)
		}
		w.op(w.width(meanWidth(line.Points)))
		w.op(w.color(c))
		w.path(line.Points)
	case penPressure, penTilt:
		w.segments(line.Points, kind, base)
	}
}

// segments draws a stroke whose width or shade changes along it, joining
// runs of segments that come out the same.
func (w *writer) segments(pts []rm.Point, kind penKind, base rgb) {
	if len(pts) == 1 {
		pts = []rm.Point{pts[0], pts[0]}
	}
	style := func(a, b rm.Point) (string, string) {
		wpx := (float64(a.Width) + float64(b.Width)) / 2
		p := clamp01((float64(a.Pressure) + float64(b.Pressure)) / 2)
		if kind == penPressure {
			return w.width(wpx * (0.5 + 0.5*p)), w.color(base)
		}
		return w.width(wpx), w.color(lighten(base, 0.4+0.6*p))
	}
	start := 0
	curW, curC := style(pts[0], pts[1])
	lastW, lastC := "", ""
	flush := func(end int) {
		if curW != lastW {
			w.op(curW)
			lastW = curW
		}
		if curC != lastC {
			w.op(curC)
			lastC = curC
		}
		w.path(pts[start : end+1])
	}
	for i := 1; i < len(pts)-1; i++ {
		sw, sc := style(pts[i], pts[i+1])
		if sw != curW || sc != curC {
			flush(i)
			start, curW, curC = i, sw, sc
		}
	}
	flush(len(pts) - 1)
}
