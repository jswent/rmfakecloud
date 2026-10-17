package v5render

import (
	"bytes"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/juruen/rmapi/encoding/rm"
)

var update = flag.Bool("update", false, "rewrite the golden files in testdata/")

const fixtureDir = "../../../../testdata/rm/v5"

func load(t *testing.T, name string) *rm.Rm {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(fixtureDir, name))
	if err != nil {
		t.Fatal(err)
	}
	var page rm.Rm
	if err := page.UnmarshalBinary(data); err != nil {
		t.Fatal(err)
	}
	return &page
}

type sizeCase struct {
	name string
	w, h float64
}

var sizes = []sizeCase{
	{"default", 447.29, 596.39},
	{"500x1000", 500, 1000},
	{"1000x500", 1000, 500},
}

func TestGolden(t *testing.T) {
	for _, fixture := range []string{"test_v3.rm", "test_v5.rm"} {
		page := load(t, fixture)
		for _, sz := range sizes {
			for _, landscape := range []bool{false, true} {
				orient := "portrait"
				if landscape {
					orient = "landscape"
				}
				name := fmt.Sprintf("%s_%s_%s", strings.TrimSuffix(fixture, ".rm"), sz.name, orient)
				t.Run(name, func(t *testing.T) {
					out, err := Render(page, Options{PageW: sz.w, PageH: sz.h, Landscape: landscape})
					if err != nil {
						t.Fatal(err)
					}
					checkStream(t, out.Content)
					golden := filepath.Join("testdata", name+".txt")
					if *update {
						if err := os.WriteFile(golden, out.Content, 0o644); err != nil {
							t.Fatal(err)
						}
						return
					}
					want, err := os.ReadFile(golden)
					if err != nil {
						t.Fatalf("%v (run go test -tags ci ./internal/storage/exporter/v5render -update)", err)
					}
					if !bytes.Equal(out.Content, want) {
						t.Errorf("content differs from %s (rerun with -update after checking)", golden)
					}
					again, _ := Render(page, Options{PageW: sz.w, PageH: sz.h, Landscape: landscape})
					if !bytes.Equal(out.Content, again.Content) {
						t.Error("output is not deterministic")
					}
				})
			}
		}
	}
}

// operand counts of every operator the stream may contain
var whitelist = map[string]int{
	"q": 0, "Q": 0, "cm": 6, "w": 1, "RG": 3, "m": 2, "l": 2, "S": 0, "J": 1, "j": 1, "gs": 1,
}

// checkStream asserts balanced q/Q, known operators with the right operand
// counts, and finite numbers.
func checkStream(t *testing.T, content []byte) {
	t.Helper()
	depth := 0
	var operands []string
	for _, tok := range strings.Fields(string(content)) {
		if strings.HasPrefix(tok, "/") {
			operands = append(operands, tok)
			continue
		}
		if v, err := strconv.ParseFloat(tok, 64); err == nil {
			if math.IsNaN(v) || math.IsInf(v, 0) {
				t.Fatalf("non-finite number %q", tok)
			}
			operands = append(operands, tok)
			continue
		}
		n, ok := whitelist[tok]
		if !ok {
			t.Fatalf("operator %q not allowed", tok)
		}
		if len(operands) != n {
			t.Fatalf("operator %s has %d operands, want %d", tok, len(operands), n)
		}
		if tok == "gs" && !strings.HasPrefix(operands[0], "/") {
			t.Fatalf("gs operand %q is not a name", operands[0])
		}
		operands = operands[:0]
		switch tok {
		case "q":
			depth++
		case "Q":
			depth--
			if depth < 0 {
				t.Fatal("Q without q")
			}
		}
	}
	if depth != 0 {
		t.Fatalf("unbalanced q/Q: depth %d at end", depth)
	}
	if len(operands) != 0 {
		t.Fatalf("trailing operands %v", operands)
	}
}

func firstMove(t *testing.T, content []byte) (x, y float64) {
	t.Helper()
	for _, line := range strings.Split(string(content), "\n") {
		if f := strings.Fields(line); len(f) == 3 && f[2] == "m" {
			x, _ = strconv.ParseFloat(f[0], 64)
			y, _ = strconv.ParseFloat(f[1], 64)
			return x, y
		}
	}
	t.Fatal("no m operator")
	return 0, 0
}

// TestKnownPoint checks the first point of test_v5.rm, (251.75104, 252.38867)
// device px, against values worked out by hand (bc) from the triage report's
// formulas with s = 72/226:
//
//	portrait 447.292x596.389:  x = 251.75104*s = 80.2039,  y = 596.389 - 252.38867*s = 515.9824
//	landscape 596.389x447.292: x = (1872-252.38867)*s = 515.9824, y = 447.292 - 251.75104*s = 367.0882
func TestKnownPoint(t *testing.T) {
	page := load(t, "test_v5.rm")
	p := page.Layers[0].Lines[0].Points[0]
	if p.X != 251.75104 || p.Y != 252.38867 {
		t.Fatalf("fixture changed: first point %v,%v", p.X, p.Y)
	}
	cases := []struct {
		name         string
		opts         Options
		wantX, wantY float64
	}{
		{"portrait", Options{PageW: DefaultPageW, PageH: DefaultPageH}, 80.2039, 515.9824},
		{"landscape", Options{PageW: DefaultPageH, PageH: DefaultPageW, Landscape: true}, 515.9824, 367.0882},
		// same page shape, orientation inferred from the page being wider than tall
		{"wide page", Options{PageW: DefaultPageH, PageH: DefaultPageW}, 515.9824, 367.0882},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, err := Render(page, c.opts)
			if err != nil {
				t.Fatal(err)
			}
			x, y := firstMove(t, out.Content)
			if math.Abs(x-c.wantX) > 0.002 || math.Abs(y-c.wantY) > 0.002 {
				t.Errorf("got %.4f %.4f, want %.4f %.4f", x, y, c.wantX, c.wantY)
			}
		})
	}
}

func TestDefaultPageSize(t *testing.T) {
	if math.Abs(DefaultPageW-447.29) > 0.005 || math.Abs(DefaultPageH-596.39) > 0.005 {
		t.Fatalf("default page %v x %v", DefaultPageW, DefaultPageH)
	}
}

func TestTransformScale(t *testing.T) {
	cases := []struct {
		opts   Options
		scale  float64
		turned bool
	}{
		{Options{PageW: 595, PageH: 842}, 842.0 / 1872, false}, // A4: ratio 1.415, fit height
		{Options{PageW: 612, PageH: 792}, 612.0 / 1404, false}, // Letter: ratio 1.294, fit width
		{Options{PageW: 960, PageH: 540}, 960.0 / 1872, true},  // giovi's deck
		{Options{PageW: 447, PageH: 596, Landscape: true}, 447.0 / 1872, true},
	}
	for _, c := range cases {
		tr := transformFor(c.opts)
		if math.Abs(tr.scale-c.scale) > 1e-12 || tr.turned != c.turned {
			t.Errorf("%+v: got scale %v turned %v, want %v %v", c.opts, tr.scale, tr.turned, c.scale, c.turned)
		}
	}
}

func line(bt rm.BrushType, col rm.BrushColor, pts ...rm.Point) rm.Line {
	return rm.Line{BrushType: bt, BrushColor: col, BrushSize: rm.Medium, Points: pts}
}

func pt(x, y, w, p float32) rm.Point { return rm.Point{X: x, Y: y, Width: w, Pressure: p} }

func TestPens(t *testing.T) {
	opts := Options{PageW: DefaultPageW, PageH: DefaultPageH}
	render := func(t *testing.T, o Options, lines ...rm.Line) *Output {
		t.Helper()
		out, err := Render(&rm.Rm{Layers: []rm.Layer{{Lines: lines}}}, o)
		if err != nil {
			t.Fatal(err)
		}
		checkStream(t, out.Content)
		return out
	}

	t.Run("erasers skipped", func(t *testing.T) {
		out := render(t, opts,
			line(rm.Eraser, 0, pt(1, 1, 4, 1), pt(2, 2, 4, 1)),
			line(rm.EraseArea, 0, pt(1, 1, 4, 1), pt(2, 2, 4, 1)))
		if got := string(out.Content); got != "q\n1 J 1 j\nQ\n" {
			t.Errorf("got %q", got)
		}
	})
	t.Run("stroke colour uses RG", func(t *testing.T) {
		out := render(t, opts, line(rm.FinelinerV5, 6, pt(1, 1, 4, 1), pt(2, 2, 4, 1)))
		s := string(out.Content)
		if !strings.Contains(s, "0.306 0.412 0.788 RG") || strings.Contains(s, " rg") {
			t.Errorf("blue stroke: %q", s)
		}
	})
	t.Run("black is black", func(t *testing.T) {
		out := render(t, opts, line(rm.BallPoint, rm.Black, pt(1, 1, 4, 1), pt(2, 2, 4, 1)))
		if !strings.Contains(string(out.Content), "\n0 0 0 RG\n") {
			t.Errorf("got %q", out.Content)
		}
	})
	t.Run("fineliner width from point width", func(t *testing.T) {
		out := render(t, opts, line(rm.Fineliner, 0, pt(1, 1, 4, 0.1), pt(2, 2, 4, 0.9)))
		want := num(4*PointsPerPixel) + " w"
		if !strings.Contains(string(out.Content), want) {
			t.Errorf("want %q in %q", want, out.Content)
		}
	})
	t.Run("brush width follows pressure", func(t *testing.T) {
		out := render(t, opts, line(rm.BrushV5, 0, pt(1, 1, 10, 0), pt(2, 2, 10, 0), pt(3, 3, 10, 1), pt(4, 4, 10, 1)))
		s := string(out.Content)
		for _, px := range []float64{5, 7.5, 10} {
			if !strings.Contains(s, num(px*PointsPerPixel)+" w") {
				t.Errorf("missing width for %v px in %q", px, s)
			}
		}
	})
	t.Run("highlighter", func(t *testing.T) {
		out := render(t, opts, line(rm.HighlighterV5, 0, pt(10, 10, 0, 1), pt(100, 10, 0, 1)))
		s := string(out.Content)
		for _, want := range []string{"/" + HighlightGState + " gs", "2 J", num(30*PointsPerPixel) + " w", "0.984 0.969 0.098 RG"} {
			if !strings.Contains(s, want) {
				t.Errorf("missing %q in %q", want, s)
			}
		}
		gs := out.Resources.ExtGState[HighlightGState]
		if gs["CA"] != 0.5 || gs["BM"] != "Multiply" {
			t.Errorf("ExtGState %v", gs)
		}
	})
	t.Run("no resources without highlighter", func(t *testing.T) {
		out := render(t, opts, line(rm.Fineliner, 0, pt(1, 1, 4, 1)))
		if out.Resources.ExtGState != nil {
			t.Errorf("unexpected resources %v", out.Resources)
		}
	})
	t.Run("gray mode", func(t *testing.T) {
		o := opts
		o.ColorMode = ColorGray
		out := render(t, o, line(rm.Fineliner, 7, pt(1, 1, 4, 1), pt(2, 2, 4, 1)))
		y := num(0.299*179/255 + 0.587*62/255 + 0.114*57/255)
		if !strings.Contains(string(out.Content), y+" "+y+" "+y+" RG") {
			t.Errorf("got %q", out.Content)
		}
	})
	t.Run("non-finite points skipped", func(t *testing.T) {
		nan := float32(math.NaN())
		out := render(t, opts, line(rm.Fineliner, 0, pt(nan, 1, 4, 1), pt(2, 2, 4, 1)))
		if strings.Contains(string(out.Content), " m") {
			t.Errorf("got %q", out.Content)
		}
	})
	t.Run("single point is a dot", func(t *testing.T) {
		render(t, opts, line(rm.BrushV5, 0, pt(1, 1, 4, 1)), line(rm.Marker, 0, pt(1, 1, 4, 1)))
	})
}

func TestBadInput(t *testing.T) {
	if _, err := Render(nil, Options{PageW: 1, PageH: 1}); err == nil {
		t.Error("nil page: no error")
	}
	for _, o := range []Options{{}, {PageW: -1, PageH: 1}, {PageW: math.Inf(1), PageH: 1}, {PageW: math.NaN(), PageH: 1}} {
		if _, err := Render(&rm.Rm{}, o); err == nil {
			t.Errorf("%+v: no error", o)
		}
	}
}
