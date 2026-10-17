package inkpdf

import (
	"image/color"
	"strings"
	"testing"
)

func TestReorder(t *testing.T) {
	bg := backgroundPDF([]pageSpec{
		{MediaBox: [4]float64{0, 0, 100, 100}},
		{MediaBox: [4]float64{0, 0, 200, 200}, Annots: true},
		{MediaBox: [4]float64{0, 0, 300, 300}},
	}, "")
	d := openBytes(t, bg)
	if err := d.StampImage(2, solidImage(10, 10, color.NRGBA{0, 0, 255, 255}), Rect{10, 10, 50, 50}, BlendMultiply); err != nil {
		t.Fatal(err)
	}
	if n, err := d.AddBlankPage(50, 60); err != nil || n != 4 {
		t.Fatalf("AddBlankPage %d %v", n, err)
	}
	if err := d.Reorder([]int{2, 4, 1}); err != nil {
		t.Fatal(err)
	}
	if d.PageCount() != 3 {
		t.Fatalf("PageCount %d", d.PageCount())
	}
	// The document stays usable after reordering.
	if err := d.StampImage(3, solidImage(4, 4, color.NRGBA{255, 0, 0, 255}), Rect{0, 0, 10, 10}, BlendNormal); err != nil {
		t.Fatal(err)
	}
	ctx := readValidated(t, writeBytes(t, d), nil)
	want := [][2]float64{{200, 200}, {50, 60}, {100, 100}}
	for i, s := range want {
		_, attrs := pageDict(t, ctx, i+1)
		if w, h := attrs.MediaBox.Width(), attrs.MediaBox.Height(); w != s[0] || h != s[1] {
			t.Errorf("page %d is %vx%v, want %vx%v", i+1, w, h, s[0], s[1])
		}
	}
	pd, _ := pageDict(t, ctx, 1)
	if !strings.Contains(strings.Join(contents(t, ctx, pd), "\n"), " Do") {
		t.Error("stamped image lost on reordered page 1")
	}
	if entry(pd, "Annots") == "" {
		t.Error("annotations lost on reordered page 1")
	}
	pd3, _ := pageDict(t, ctx, 3)
	if !strings.Contains(strings.Join(contents(t, ctx, pd3), "\n"), " Do") {
		t.Error("image stamped after Reorder missing on page 3")
	}
}

func TestReorderIdentityAndErrors(t *testing.T) {
	d := openBytes(t, backgroundPDF([]pageSpec{{MediaBox: [4]float64{0, 0, 100, 100}}, {MediaBox: [4]float64{0, 0, 100, 100}}}, ""))
	ctx := d.ctx
	if err := d.Reorder([]int{1, 2}); err != nil || d.ctx != ctx {
		t.Fatalf("identity reorder changed the document: %v", err)
	}
	if err := d.Reorder([]int{2, 2, 1}); err != nil || d.PageCount() != 3 {
		t.Fatalf("repeat: %d pages, %v", d.PageCount(), err)
	}
	readValidated(t, writeBytes(t, d), nil)
	for _, bad := range [][]int{nil, {0}, {4}} {
		if err := d.Reorder(bad); err == nil {
			t.Errorf("Reorder(%v) accepted", bad)
		}
	}
}
