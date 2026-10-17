package exporter

import (
	"image"
	"image/color"
	"testing"

	"github.com/ddvk/rmfakecloud/internal/storage/exporter/content"
	"github.com/ddvk/rmfakecloud/internal/storage/exporter/rmlines"
)

func white(r image.Rectangle) *image.Gray {
	g := image.NewGray(r)
	for i := range g.Pix {
		g.Pix[i] = 255
	}
	return g
}

// flatten: ink over the background by its alpha, highlights multiplied by
// theirs, every pixel opaque; it works on a sub-image too.
func TestFlatten(t *testing.T) {
	full := image.NewNRGBA(image.Rect(0, 0, 8, 8))
	r := image.Rect(2, 2, 6, 6)
	ink := full.SubImage(r).(*image.NRGBA)
	hl := image.NewNRGBA(r)
	bg := white(r)
	bg.SetGray(3, 3, color.Gray{192}) // a template rule
	bg.SetGray(4, 4, color.Gray{192})
	bg.SetGray(5, 5, color.Gray{192})

	ink.SetNRGBA(2, 2, color.NRGBA{0, 0, 0, 255})     // black ink
	ink.SetNRGBA(3, 2, color.NRGBA{0, 0, 0, 128})     // half-covered edge
	ink.SetNRGBA(3, 3, color.NRGBA{255, 0, 0, 255})   // red ink over a rule
	ink.SetNRGBA(4, 4, color.NRGBA{0, 0, 0, 128})     // edge over a rule
	hl.SetNRGBA(2, 3, color.NRGBA{255, 255, 0, 255})  // yellow highlight on white
	hl.SetNRGBA(5, 5, color.NRGBA{255, 255, 0, 255})  // ... over a rule
	ink.SetNRGBA(2, 4, color.NRGBA{0, 0, 0, 255})     // black ink under a highlight
	hl.SetNRGBA(2, 4, color.NRGBA{255, 255, 0, 255})  //
	ink.SetNRGBA(5, 2, color.NRGBA{100, 100, 100, 0}) // transparent: colour ignored
	hl.SetNRGBA(4, 2, color.NRGBA{255, 255, 0, 128})  // half-covered highlight

	flatten(ink, hl, bg)

	want := map[image.Point]color.NRGBA{
		{2, 2}: {0, 0, 0, 255},
		{3, 2}: {127, 127, 127, 255},
		{3, 3}: {255, 0, 0, 255},
		{4, 4}: {96, 96, 96, 255},    // 192 * 127/255
		{2, 3}: {255, 255, 0, 255},   // white * yellow
		{5, 5}: {192, 192, 0, 255},   // rule * yellow
		{2, 4}: {0, 0, 0, 255},       // black stays black under yellow
		{5, 2}: {255, 255, 255, 255}, // background shows
		{4, 2}: {255, 255, 127, 255}, // half yellow: blue channel halved
		{5, 4}: {255, 255, 255, 255},
	}
	for p, w := range want {
		if got := ink.NRGBAAt(p.X, p.Y); got != w {
			t.Errorf("pixel %v: got %v, want %v", p, got, w)
		}
	}
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			if ink.NRGBAAt(x, y).A != 255 {
				t.Errorf("pixel %d,%d not opaque", x, y)
			}
		}
	}
	// Pixels of the parent outside the sub-image are untouched.
	if full.NRGBAAt(0, 0) != (color.NRGBA{}) || full.NRGBAAt(7, 7) != (color.NRGBA{}) {
		t.Error("flatten wrote outside the sub-image")
	}
}

// pageBackground paints the page template at the exporter's scale, and
// nothing with RMLINES_TEMPLATES=false or without a template.
func TestPageBackground(t *testing.T) {
	j := &job{page: content.PlanPage{Template: "P Lined heading"}, frame: rmlines.Extent{L: 0, T: 0, R: 1404, B: 1872}}
	e := New(Config{Disable: true})
	bg := e.pageBackground(j, image.Rect(0, 0, 1404, 1872), false)
	if v := bg.GrayAt(700, 287).Y; v != 192 {
		t.Errorf("rule pixel %d, want 192", v)
	}
	if v := bg.GrayAt(700, 300).Y; v != 255 {
		t.Errorf("page pixel %d, want 255", v)
	}
	// At scale 2 the image is twice as large and the rule is 1 px wide.
	e = New(Config{Disable: true, Scale: 2})
	bg = e.pageBackground(j, image.Rect(0, 0, 2808, 3744), false)
	if a, b := bg.GrayAt(1400, 573).Y, bg.GrayAt(1400, 574).Y; a != 224 || b != 224 {
		t.Errorf("scale 2 rule rows %d %d, want 224 224", a, b)
	}
	for _, e := range []*Exporter{New(Config{Disable: true, NoTemplates: true})} {
		bg = e.pageBackground(j, image.Rect(0, 0, 1404, 1872), false)
		if v := bg.GrayAt(700, 287).Y; v != 255 {
			t.Errorf("NoTemplates: rule pixel %d, want 255", v)
		}
	}
	j.page.Template = ""
	bg = New(Config{Disable: true}).pageBackground(j, image.Rect(0, 0, 1404, 1872), false)
	if v := bg.GrayAt(700, 287).Y; v != 255 {
		t.Errorf("no template: rule pixel %d, want 255", v)
	}
}
