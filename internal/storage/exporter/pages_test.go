package exporter

import (
	"image"
	"math"
	"testing"

	"github.com/ddvk/rmfakecloud/internal/storage/exporter/inkpdf"
	"github.com/ddvk/rmfakecloud/internal/storage/exporter/rmlines"
)

func TestPageSizes(t *testing.T) {
	g := geometry{ppu: DefaultPtPerUnit, scale: 1, clip: true}
	// The tablet's own exports: rM2 445x594, Paper Pro 514x685, Move 260x463.
	for _, tt := range []struct {
		w, h   int
		ww, wh float64
	}{
		{1404, 1872, 445.3, 593.8},
		{1620, 2160, 513.8, 685.1},
		{820, 1458, 260.1, 462.4},
	} {
		w, h := g.nominalSize(tt.w, tt.h, false)
		if w != tt.ww || h != tt.wh {
			t.Errorf("%dx%d: %vx%v, want %vx%v", tt.w, tt.h, w, h, tt.ww, tt.wh)
		}
	}
	if w, h := g.nominalSize(1404, 1872, true); w != 593.8 || h != 445.3 {
		t.Errorf("landscape: %vx%v", w, h)
	}
	// AI training page 1: ink down to 6280, +48 pad -> 6328 units -> 2007.1 pt (tablet: 2007).
	if h := g.pt(6280 + tabletPad); h != 2007.1 {
		t.Errorf("grown page height %v, want 2007.1", h)
	}
}

func TestNotebookFrame(t *testing.T) {
	res := &rmlines.Result{PageW: 1404, PageH: 1872, Extent: rmlines.Extent{L: -100, T: -50, R: 1500, B: 3000}}
	clip := geometry{ppu: DefaultPtPerUnit, scale: 0.5, clip: true}
	f := clip.notebookFrame(res)
	if f != (rmlines.Extent{L: 0, T: 0, R: 1404, B: 3000}) {
		t.Fatalf("clipped frame %+v", f)
	}
	img := image.Rect(0, 0, 800, 1525) // (1600 x 3050) * 0.5
	if r := clip.cropRect(img, res.Extent, f); r != image.Rect(50, 25, 752, 1525) {
		t.Fatalf("crop %v", r)
	}
	// A page that did not grow keeps its nominal height.
	small := &rmlines.Result{PageW: 1404, PageH: 1872, Extent: rmlines.Extent{L: 0, T: 0, R: 1404, B: 1872}}
	if f := clip.notebookFrame(small); f.B != 1872 {
		t.Fatalf("frame %+v", f)
	}
	noclip := clip
	noclip.clip = false
	if f := noclip.notebookFrame(res); f != res.Extent {
		t.Fatalf("unclipped frame %+v", f)
	}
	if r := noclip.cropRect(img, res.Extent, res.Extent); r != img {
		t.Fatalf("unclipped crop %v", r)
	}
}

func TestV6Placement(t *testing.T) {
	ppu := DefaultPtPerUnit
	near := func(a, b float64) bool { return math.Abs(a-b) < 1e-9 }
	for _, tt := range []struct {
		name  string
		ext   rmlines.Extent
		pageW int
		vw    float64
		want  inkpdf.Rect
	}{
		// On a page exactly the device's width, the frame covers the page.
		{"device width", rmlines.Extent{L: 0, T: 0, R: 1404, B: 1872}, 1404, 1404 * ppu,
			inkpdf.Rect{X: 0, Y: 0, W: 1404 * ppu, H: 1872 * ppu}},
		// On a wider page (A4 landscape-ish 842 pt), the device frame is centred.
		{"centred", rmlines.Extent{L: 0, T: 0, R: 1404, B: 1872}, 1404, 842,
			inkpdf.Rect{X: 421 - 702*ppu, Y: 0, W: 1404 * ppu, H: 1872 * ppu}},
		// Ink grown to the left and down keeps its scale and origin.
		{"grown", rmlines.Extent{L: -100, T: 0, R: 1404, B: 2500}, 1404, 612,
			inkpdf.Rect{X: 306 - 802*ppu, Y: 0, W: 1504 * ppu, H: 2500 * ppu}},
		// Landscape: rmlines swapped the frame, so pageW is the long side.
		{"landscape", rmlines.Extent{L: 0, T: 0, R: 1872, B: 1404}, 1872, 960,
			inkpdf.Rect{X: 480 - 936*ppu, Y: 0, W: 1872 * ppu, H: 1404 * ppu}},
	} {
		got := v6Placement(tt.ext, tt.pageW, tt.vw, ppu)
		if !near(got.X, tt.want.X) || !near(got.Y, tt.want.Y) || !near(got.W, tt.want.W) || !near(got.H, tt.want.H) {
			t.Errorf("%s: %+v, want %+v", tt.name, got, tt.want)
		}
	}
}

func TestHasAlpha(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	if hasAlpha(img) {
		t.Fatal("empty image has alpha")
	}
	img.Pix[img.PixOffset(3, 3)+3] = 1
	if !hasAlpha(img) {
		t.Fatal("pixel missed")
	}
	if hasAlpha(img.SubImage(image.Rect(0, 0, 3, 3)).(*image.NRGBA)) {
		t.Fatal("sub-image sees pixel outside it")
	}
}
