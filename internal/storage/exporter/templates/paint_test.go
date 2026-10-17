package templates

import (
	"image"
	"math"
	"testing"
)

func whitePage(w, h int) *image.Gray {
	g := image.NewGray(image.Rect(0, 0, w, h))
	for i := range g.Pix {
		g.Pix[i] = 255
	}
	return g
}

// column returns the grey levels of column x, rows y0..y1-1.
func column(g *image.Gray, x, y0, y1 int) []uint8 {
	out := make([]uint8, 0, y1-y0)
	for y := y0; y < y1; y++ {
		out = append(out, g.GrayAt(x, y).Y)
	}
	return out
}

// At one pixel per unit a rule 0.5 units wide is thinner than a pixel and
// is drawn as one full-grey row, the row holding its centre (what poppler
// draws for the vector page at 227 dpi: row 287 at level 192); the rest of
// the column stays white.
func TestPaintLinedHeading(t *testing.T) {
	g := whitePage(1404, 1872)
	if !Paint("P Lined heading", g, 1404, 1872, false, 1, 0, 0) {
		t.Fatal("not painted")
	}
	// First rule at y 287; the next at 365.7 lands on row 365.
	if got := column(g, 700, 285, 290); !equal(got, []uint8{255, 255, 192, 255, 255}) {
		t.Errorf("rows 285..289 at x 700: %v", got)
	}
	if got := column(g, 700, 364, 367); !equal(got, []uint8{255, 192, 255}) {
		t.Errorf("rows 364..366 at x 700: %v", got)
	}
	// Heading at y 187 from x 218 to 1186: present at x 700, absent at x 100.
	if g.GrayAt(700, 187).Y != 192 || g.GrayAt(100, 187).Y != 255 {
		t.Errorf("heading: %d at x 700, %d at x 100", g.GrayAt(700, 187).Y, g.GrayAt(100, 187).Y)
	}
	// The square cap extends 0.25 units past the end: pixel 217 gets a
	// quarter (255 - 63/4 = 239), pixel 216 nothing.
	if g.GrayAt(217, 187).Y != 239 || g.GrayAt(216, 187).Y != 255 {
		t.Errorf("heading cap: %d %d", g.GrayAt(217, 187).Y, g.GrayAt(216, 187).Y)
	}
	// 21 rules on an rM2 page (the heading does not reach x 100), one row each.
	touched := 0
	for _, v := range column(g, 100, 0, 1872) {
		if v != 255 {
			touched++
		}
	}
	if touched != 21 {
		t.Errorf("%d touched rows at x 100, want 21", touched)
	}
}

// At 2 pixels per unit the rule is a pixel wide and gets area coverage:
// y 287 covers pixel rows 573.5 .. 574.5 of an image that starts at unit
// 250, so rows 73 and 74 get half coverage each (224).
func TestPaintScaleAndOrigin(t *testing.T) {
	g := whitePage(200, 200)
	// The image shows units [50, 150) x [250, 350) of the page at 2 px/unit.
	if !Paint("P Lined heading", g, 1404, 1872, false, 2, 50, 250) {
		t.Fatal("not painted")
	}
	if got := column(g, 100, 72, 76); !equal(got, []uint8{255, 224, 224, 255}) {
		t.Errorf("rows 72..75: %v", got)
	}
}

// Dots are 2x2 unit grey squares; a crossing of two rules is no darker
// than one rule; unknown and blank templates leave the page alone.
func TestPaintDotsGridBlank(t *testing.T) {
	g := whitePage(1404, 1872)
	Paint("P Dots large", g, 1404, 1872, false, 1, 0, 0)
	// Dots at canvas x -3 + 78k -> page x -237 + 78k: k=4 is 75; y 78.
	// The square [74.5, 76.5] x [77.5, 79.5] covers pixels 75 and 78 fully.
	if v := g.GrayAt(75, 78).Y; v != 192 {
		t.Errorf("dot centre %d, want 192", v)
	}
	if v := g.GrayAt(74, 78).Y; v != 224 {
		t.Errorf("dot edge %d, want 224", v)
	}
	if v := g.GrayAt(72, 78).Y; v != 255 {
		t.Errorf("beside the dot %d, want 255", v)
	}

	g = whitePage(1404, 1872)
	Paint("P Grid medium", g, 1404, 1872, false, 1, 0, 0)
	// Vertical line at x 0.9+78.3*5 = 392.4 is column 392; the horizontal
	// at y 78.3*5 = 391.5 is row 391. The crossing is one rule grey, not
	// darker.
	x, y := 0.9+78.3*5, 78.3*5
	cx, cy := int(math.Floor(x)), int(math.Floor(y))
	if v := g.GrayAt(cx+1, cy).Y; v != 192 {
		t.Errorf("horizontal rule %d, want 192", v)
	}
	if v := g.GrayAt(cx, cy).Y; v != 192 {
		t.Errorf("crossing %d, want 192", v)
	}

	for _, name := range []string{"Blank", "P Blank", "P Weird unknown"} {
		g = whitePage(10, 10)
		if Paint(name, g, 1404, 1872, false, 1, 0, 0) {
			t.Errorf("%s painted", name)
		}
		for _, v := range g.Pix {
			if v != 255 {
				t.Fatalf("%s touched the page", name)
			}
		}
	}
}

// Nothing is drawn outside the page: the rules of a landscape page are
// clipped at the page edge, and an image that shows more than the page
// (RMLINES_CLIP=false) stays white there.
func TestPaintClip(t *testing.T) {
	g := whitePage(100, 100)
	// The image shows units [-50, 50) x [-50, 50): only the top-left
	// quarter is page.
	Paint("P Grid medium", g, 1404, 1872, false, 1, -50, -50)
	for y := 0; y < 100; y++ {
		for x := 0; x < 100; x++ {
			if (x < 50 || y < 50) && g.GrayAt(x, y).Y != 255 {
				t.Fatalf("pixel %d,%d outside the page is %d", x, y, g.GrayAt(x, y).Y)
			}
		}
	}
	// The grid's line at y 0 lies half outside the page; what is left is
	// thinner than a pixel and fills row 50.
	if v := g.GrayAt(70, 50).Y; v != 192 {
		t.Errorf("edge rule %d, want 192", v)
	}
}

func equal(a, b []uint8) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
