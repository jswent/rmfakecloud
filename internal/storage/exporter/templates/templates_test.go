package templates

import (
	"context"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
)

// Page sizes in points, as the exporter makes them (page units * 72/227,
// rounded to 0.1 pt).
var (
	rm2  = [2]float64{445.3, 593.7}
	pp   = [2]float64{513.8, 685.1}
	move = [2]float64{260.1, 462.5}
)

func TestLinedHeading(t *testing.T) {
	tests := []struct {
		name     string
		size     [2]float64
		rules    int // heading included
		headL    float64
		lastRule float64 // y of the lowest rule, page units
	}{
		{"rM2", rm2, 22, 218, 1861},
		{"Paper Pro", pp, 25, 218, 2097.1},
		{"Move", move, 16, move[0]/DefaultPtPerUnit - headingXMove, 1388.8}, // 120.0 here, 120.3 on the tablet's 820.3-unit canvas
		// AI training page 1: a Paper Pro page grown to 2007 pt.
		{"Paper Pro grown", [2]float64{513.8, 2007}, 78, 218, 6268.2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _, ok := Render("P Lined heading", tt.size[0], tt.size[1], false)
			if !ok {
				t.Fatal("not drawn")
			}
			h := horizontal(greyRules(c))
			if len(h) != tt.rules {
				t.Fatalf("%d rules, want %d", len(h), tt.rules)
			}
			unit := func(yPt float64) float64 { return (tt.size[1] - yPt) / DefaultPtPerUnit }
			if y := unit(h[0].y0); math.Abs(y-headingY) > 0.01 {
				t.Errorf("heading at y %v, want %v", y, headingY)
			}
			if x := h[0].x0 / DefaultPtPerUnit; math.Abs(x-tt.headL) > 0.01 {
				t.Errorf("heading starts at x %v, want %v", x, tt.headL)
			}
			if r := h[0].x1; math.Abs(r-(tt.size[0]-h[0].x0)) > 0.01 {
				t.Errorf("heading is not centred: %v..%v on %v", h[0].x0, r, tt.size[0])
			}
			for i, s := range h[1:] {
				want := linedFirstY + float64(i)*linedSpacing
				if y := unit(s.y0); math.Abs(y-want) > 0.01 {
					t.Errorf("rule %d at y %v, want %v", i+1, y, want)
				}
				if s.x0 != 0 || math.Abs(s.x1-tt.size[0]) > 0.01 {
					t.Errorf("rule %d spans %v..%v, want the page width", i+1, s.x0, s.x1)
				}
			}
			if y := unit(h[len(h)-1].y0); math.Abs(y-tt.lastRule) > 0.01 {
				t.Errorf("last rule at %v, want %v", y, tt.lastRule)
			}
			if w := h[0].width; math.Abs(w-0.5*DefaultPtPerUnit) > 1e-4 {
				t.Errorf("stroke width %v pt", w)
			}
		})
	}
}

func TestBlankAndUnknown(t *testing.T) {
	for _, name := range []string{"Blank", "P Blank", "", "No such template"} {
		if c, _, ok := Render(name, pp[0], pp[1], false); ok || c != nil {
			t.Errorf("%q: drawn", name)
		}
	}
}

// TestAllTemplates draws every known template on every device, portrait and
// landscape, and checks the stream is balanced and draws inside the page.
func TestAllTemplates(t *testing.T) {
	for _, name := range Names() {
		s, _ := Lookup(name)
		for _, size := range [][2]float64{rm2, pp, move} {
			w, h := size[0], size[1]
			land := strings.HasPrefix(name, "LS ")
			if land {
				w, h = h, w
			}
			c, _, ok := Render(name, w, h, land)
			if ok != (s.Kind != Blank) {
				t.Errorf("%s on %vx%v: ok %v", name, w, h, ok)
				continue
			}
			if !ok {
				continue
			}
			depth := 0
			for _, tok := range tokenRE.FindAllString(string(c), -1) {
				switch tok {
				case "q":
					depth++
				case "Q":
					depth--
				}
				if depth < 0 {
					t.Fatalf("%s: unbalanced Q", name)
				}
			}
			if depth != 0 {
				t.Errorf("%s: unbalanced q", name)
			}
			if s.Kind == Lines || s.Kind == Grid {
				if n := len(greyRules(c)); n < 5 {
					t.Errorf("%s on %vx%v: %d rules", name, w, h, n)
				}
			}
		}
	}
}

// Tablet exports in testdata/rm/device (not in the repository): every page
// uses "P Lined heading".
const deviceDir = "../../../../testdata/rm/device"

// TestDeviceGeometry compares the rules Render draws with the rules in the
// tablet's own PDF export, page by page.
func TestDeviceGeometry(t *testing.T) {
	pages := []struct {
		file string
		page int
	}{
		{"AI training.pdf", 3}, // Paper Pro
		{"Sites.pdf", 3},       // reMarkable 2
		{"Move.pdf", 2},        // Paper Pro Move
		{"AI training.pdf", 1}, // Paper Pro, grown to 2007 pt
		{"Sites.pdf", 2},       // reMarkable 2, grown
		{"Move.pdf", 3},        // Move, grown
	}
	for _, p := range pages {
		path := filepath.Join(deviceDir, p.file)
		if _, err := os.Stat(path); err != nil {
			t.Skipf("%s missing", path)
		}
		t.Run(p.file+" page "+string(rune('0'+p.page)), func(t *testing.T) {
			content, w, h := tabletPage(t, path, p.page)
			theirs := horizontal(greyRules(content))
			c, _, ok := Render("P Lined heading", w, h, false)
			if !ok {
				t.Fatal("not drawn")
			}
			ours := horizontal(greyRules(c))
			if len(ours) != len(theirs) {
				t.Fatalf("%d rules, tablet %d", len(ours), len(theirs))
			}
			clip := func(v float64) float64 { return math.Max(0, math.Min(w, v)) }
			for i := range ours {
				o, d := ours[i], theirs[i]
				if math.Abs(o.y0-d.y0) > 0.5 {
					t.Errorf("rule %d at %.2f pt, tablet %.2f", i, o.y0, d.y0)
				}
				if math.Abs(clip(o.x0)-clip(d.x0)) > 0.5 || math.Abs(clip(o.x1)-clip(d.x1)) > 0.5 {
					t.Errorf("rule %d spans %.2f..%.2f pt, tablet %.2f..%.2f", i, o.x0, o.x1, clip(d.x0), clip(d.x1))
				}
				if math.Abs(o.width-d.width) > 0.01 || math.Abs(o.grey-d.grey) > 0.005 {
					t.Errorf("rule %d: width %.3f grey %.3f, tablet %.3f %.3f", i, o.width, o.grey, d.width, d.grey)
				}
			}
			t.Logf("%d rules on %vx%v pt match", len(ours), w, h)
		})
	}
}

// tabletPage returns the content stream and size of a page of a PDF.
func tabletPage(t *testing.T, path string, page int) ([]byte, float64, float64) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	conf := model.NewDefaultConfiguration()
	conf.ValidationMode = model.ValidationRelaxed
	ctx, err := api.ReadAndValidate(context.Background(), f, conf)
	if err != nil {
		t.Fatal(err)
	}
	dims, err := ctx.PageDims(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	r, err := pdfcpu.ExtractPageContent(context.Background(), ctx, page)
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return b, dims[page-1].Width, dims[page-1].Height
}

// vertical returns the x positions (in page units) of the vertical rules.
func vertical(segs []segment) []float64 {
	var xs []float64
	for _, s := range segs {
		if math.Abs(s.x0-s.x1) < 1e-6 {
			xs = append(xs, s.x0/DefaultPtPerUnit)
		}
	}
	return xs
}

// TestGenerators checks the template-file geometry on rM2 pages (1404 x
// 1872 units, canvas origin -234).
func TestGenerators(t *testing.T) {
	// The page is 1403.93 units wide (445.3 pt), so the canvas origin is
	// -234.03.
	near := func(a, b float64) bool { return math.Abs(a-b) < 0.1 }
	t.Run("margin", func(t *testing.T) {
		c, _, _ := Render("P Margin medium", rm2[0], rm2[1], false)
		segs := greyRules(c)
		v := vertical(segs)
		if len(v) != 1 || !near(v[0], -234+478) {
			t.Errorf("margin rules at %v, want [244]", v)
		}
		// 177.8 + 78.7k < 1872.4: 22 rules.
		if n := len(horizontal(segs)); n != 22 {
			t.Errorf("%d rules, want 22", n)
		}
	})
	t.Run("move lines start at 160", func(t *testing.T) {
		c, _, _ := Render("P Lines medium", move[0], move[1], false)
		h := horizontal(greyRules(c))
		if y := (move[1] - h[0].y0) / DefaultPtPerUnit; !near(y, 160) {
			t.Errorf("first rule at %v, want 160", y)
		}
	})
	t.Run("grid medium", func(t *testing.T) {
		c, _, _ := Render("P Grid medium", rm2[0], rm2[1], false)
		segs := greyRules(c)
		v := vertical(segs)
		// -234 + 3*78.3 = 0.9, then every 78.3 up to 1404.
		if len(v) != 18 || !near(v[0], 0.9) || !near(v[17], 0.9+17*78.3) {
			t.Errorf("vertical lines %v", v)
		}
		if n := len(horizontal(segs)); n != 24 { // 0 .. 23*78.3 = 1800.9
			t.Errorf("%d horizontal lines, want 24", n)
		}
	})
	t.Run("grid margin", func(t *testing.T) {
		c, _, _ := Render("P Grid margin large", rm2[0], rm2[1], false)
		segs := greyRules(c)
		v := vertical(segs)
		if !near(v[0], 263) { // -234 + 497
			t.Errorf("grid starts at %v, want 263", v[0])
		}
		for _, s := range horizontal(segs) {
			if !near(s.x0/DefaultPtPerUnit, 263) {
				t.Fatalf("horizontal line starts at %v", s.x0/DefaultPtPerUnit)
			}
		}
	})
	t.Run("landscape lines", func(t *testing.T) {
		c, _, _ := Render("LS Lines small", rm2[1], rm2[0], true)
		h := horizontal(greyRules(c))
		// 0, 52, ..., 1352 < 1404: 27 rules, across the whole width.
		if len(h) != 27 || h[0].x1 < rm2[1]-0.01 {
			t.Errorf("%d rules, first spans %v..%v", len(h), h[0].x0, h[0].x1)
		}
	})
	t.Run("dots", func(t *testing.T) {
		c, _, _ := Render("P Dots large", rm2[0], rm2[1], false)
		// Columns at -237 + 78k in [0, 1404): 75, 153, ..., 1401 (18);
		// rows at 0, 78, ..., 1794 below 1871.8 (24).
		if n := strings.Count(string(c), " 1 1 re\n"); n != 18*24 {
			t.Errorf("%d dots, want %d", n, 18*24)
		}
		if !strings.Contains(string(c), "\n74.966 0 1 1 re\n") {
			t.Error("first dot not at x 75")
		}
	})
}
