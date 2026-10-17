package inkpdf

import (
	"image"
	"image/color"
	"strings"
	"testing"

	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

// stampCases cover /Rotate 0/90/180/270/-90, offset MediaBoxes, a CropBox
// inside and one partly outside the MediaBox, offset rectangles and uneven
// stretch. Expected matrices are written out by hand from the table on
// pageGeom.placement in stamp.go.
var stampCases = []struct {
	name     string
	page     pageSpec
	img      *image.NRGBA
	rect     *Rect // nil: whole visual page
	vw, vh   float64
	wantCM   matrix
	wantRed  *image.Rectangle // red extent in visual pixels; nil: quadrant check
	wantQuad string
}{
	{name: "rot0", page: pageSpec{MediaBox: [4]float64{0, 0, 200, 300}, Annots: true},
		vw: 200, vh: 300, wantCM: matrix{200, 0, 0, 300, 0, 0}},
	{name: "rot0 offset mediabox", page: pageSpec{MediaBox: [4]float64{100, 50, 300, 350}, Annots: true},
		vw: 200, vh: 300, wantCM: matrix{200, 0, 0, 300, 100, 50}},
	{name: "cropbox inside mediabox", page: pageSpec{MediaBox: [4]float64{0, 0, 400, 500}, CropBox: box(50, 100, 250, 400)},
		vw: 200, vh: 300, wantCM: matrix{200, 0, 0, 300, 50, 100}},
	{name: "cropbox partly outside mediabox", page: pageSpec{MediaBox: [4]float64{0, 0, 200, 300}, CropBox: box(-50, -50, 150, 250)},
		vw: 150, vh: 250, wantCM: matrix{150, 0, 0, 250, 0, 0}},
	{name: "rot90", page: pageSpec{MediaBox: [4]float64{0, 0, 300, 200}, Rotate: 90, Annots: true},
		vw: 200, vh: 300, wantCM: matrix{0, 200, -300, 0, 300, 0}},
	{name: "rot90 offset boxes", page: pageSpec{MediaBox: [4]float64{10, 20, 410, 320}, CropBox: box(60, 40, 360, 240), Rotate: 90},
		vw: 200, vh: 300, wantCM: matrix{0, 200, -300, 0, 360, 40}},
	{name: "rot180 offset mediabox", page: pageSpec{MediaBox: [4]float64{30, 40, 230, 340}, Rotate: 180, Annots: true},
		vw: 200, vh: 300, wantCM: matrix{-200, 0, 0, -300, 230, 340}},
	{name: "rot270", page: pageSpec{MediaBox: [4]float64{0, 0, 300, 200}, Rotate: 270},
		vw: 200, vh: 300, wantCM: matrix{0, -200, 300, 0, 0, 200}},
	{name: "rot-90 offset boxes", page: pageSpec{MediaBox: [4]float64{5, 5, 305, 205}, CropBox: box(15, 25, 295, 195), Rotate: -90},
		vw: 170, vh: 280, wantCM: matrix{0, -170, 280, 0, 15, 195}},
	{name: "offset rect", page: pageSpec{MediaBox: [4]float64{0, 0, 200, 300}},
		rect: &Rect{X: 20, Y: 30, W: 100, H: 150}, vw: 200, vh: 300,
		wantCM: matrix{100, 0, 0, 150, 20, 120}, wantRed: &image.Rectangle{image.Pt(20, 30), image.Pt(70, 105)}},
	{name: "offset rect rot90", page: pageSpec{MediaBox: [4]float64{0, 0, 300, 200}, Rotate: 90},
		rect: &Rect{X: 20, Y: 30, W: 100, H: 150}, vw: 200, vh: 300,
		wantCM: matrix{0, 100, -150, 0, 180, 20}, wantRed: &image.Rectangle{image.Pt(20, 30), image.Pt(70, 105)}},
	{name: "offset rect rot180", page: pageSpec{MediaBox: [4]float64{30, 40, 230, 340}, Rotate: 180},
		rect: &Rect{X: 20, Y: 30, W: 100, H: 150}, vw: 200, vh: 300,
		wantCM: matrix{-100, 0, 0, -150, 210, 220}, wantRed: &image.Rectangle{image.Pt(20, 30), image.Pt(70, 105)}},
	{name: "offset rect rot270", page: pageSpec{MediaBox: [4]float64{0, 0, 300, 200}, Rotate: 270},
		rect: &Rect{X: 20, Y: 30, W: 100, H: 150}, vw: 200, vh: 300,
		wantCM: matrix{0, -100, 150, 0, 120, 180}, wantRed: &image.Rectangle{image.Pt(20, 30), image.Pt(70, 105)}},
	{name: "uneven stretch", page: pageSpec{MediaBox: [4]float64{0, 0, 200, 300}},
		img: quadrantImage(500, 500), vw: 200, vh: 300,
		wantCM: matrix{200, 0, 0, 300, 0, 0}, wantRed: &image.Rectangle{image.Pt(0, 0), image.Pt(100, 150)}},
}

func stampFixture() []byte {
	specs := make([]pageSpec, len(stampCases))
	for i, c := range stampCases {
		specs[i] = c.page
	}
	return backgroundPDF(specs, "")
}

func stampAll(t *testing.T, bg []byte, img func(i int) *image.NRGBA) []byte {
	d := openBytes(t, bg)
	for i, c := range stampCases {
		w, h, err := d.PageSize(i + 1)
		if err != nil {
			t.Fatal(err)
		}
		if w != c.vw || h != c.vh {
			t.Errorf("%s: PageSize = %vx%v, want %vx%v", c.name, w, h, c.vw, c.vh)
		}
		r := Rect{0, 0, w, h}
		if c.rect != nil {
			r = *c.rect
		}
		if err := d.StampImage(i+1, img(i), r, BlendNormal); err != nil {
			t.Fatalf("%s: StampImage: %v", c.name, err)
		}
	}
	return writeBytes(t, d)
}

func caseImage(i int) *image.NRGBA {
	if stampCases[i].img != nil {
		return stampCases[i].img
	}
	return quadrantImage(400, 600)
}

// TestStampStructure verifies placement without poppler: the appended
// stream's cm matrix, unchanged boxes/Rotate/Annots and original content,
// and that pdfcpu validates the output.
func TestStampStructure(t *testing.T) {
	bg := stampFixture()
	out := stampAll(t, bg, caseImage)
	orig := readValidated(t, bg, nil)
	ctx := readValidated(t, out, nil)
	if ctx.PageCount != len(stampCases) {
		t.Fatalf("page count %d", ctx.PageCount)
	}
	for i, c := range stampCases {
		t.Run(c.name, func(t *testing.T) {
			last := checkStamped(t, orig, ctx, i+1, 1)
			if got := cmOf(t, last); !sameMatrix(got, c.wantCM) {
				t.Errorf("cm = %v, want %v", got, c.wantCM)
			}
			name := xobjectName(t, last)
			if !strings.HasPrefix(name, xobjectPrefix) {
				t.Errorf("xobject name %q", name)
			}
			_, attrs := pageDict(t, ctx, i+1)
			ref := resourceEntry(t, ctx, attrs.Resources, "XObject", name)
			sd, _, err := ctx.DereferenceStreamDict(ref)
			if err != nil || sd == nil {
				t.Fatalf("image: %v", err)
			}
			if sd.Type() != nil && *sd.Type() != "XObject" || *sd.Subtype() != "Image" {
				t.Errorf("not an image xobject: %v", sd.Dict)
			}
			if sd.Dict["SMask"] == nil {
				t.Errorf("quadrant image has no SMask")
			}
			if strings.Contains(last, " gs") {
				t.Errorf("normal blend must not set an ExtGState: %q", last)
			}
		})
	}
}

// TestStampRender verifies placement with poppler.
func TestStampRender(t *testing.T) {
	needPoppler(t)
	bg := stampFixture()
	out := stampAll(t, bg, caseImage)
	for i, c := range stampCases {
		t.Run(c.name, func(t *testing.T) {
			img := render(t, out, i+1)
			if sz := imgRect(img).Size(); sz.X != int(c.vw) || sz.Y != int(c.vh) {
				t.Fatalf("render size %v, want %vx%v", sz, c.vw, c.vh)
			}
			if c.wantRed != nil {
				if got := extent(img, "red"); !near(got, *c.wantRed, 1) {
					t.Errorf("red extent %v, want %v", got, *c.wantRed)
				}
				return
			}
			want := "TL=red TR=green BL=green BR=blue"
			if got := quadrants(img); !strings.HasSuffix(got, want) {
				t.Errorf("quadrants %s, want %s", got, want)
			}
		})
	}
}

// TestStampTransparentKeepsBackground: stamping a fully transparent image
// must not change a single background pixel (the stock watermark API
// changed 10k-29k pixels on rotated pages with offset boxes).
func TestStampTransparentKeepsBackground(t *testing.T) {
	needPoppler(t)
	bg := stampFixture()
	out := stampAll(t, bg, func(int) *image.NRGBA { return solidImage(40, 60, clear) })
	for i, c := range stampCases {
		if n := diff(render(t, bg, i+1), render(t, out, i+1)); n != 0 {
			t.Errorf("%s: %d background pixels changed", c.name, n)
		}
	}
}

func TestStampMultiply(t *testing.T) {
	grey := solidImage(20, 30, color.NRGBA{128, 128, 128, 255})
	pages := []pageSpec{
		{MediaBox: [4]float64{0, 0, 200, 300}},
		{MediaBox: [4]float64{0, 0, 200, 300}},
		{MediaBox: [4]float64{0, 0, 300, 200}, Rotate: 90},
	}
	bg := backgroundPDF(pages, "")
	d := openBytes(t, bg)
	r := Rect{X: 50, Y: 50, W: 100, H: 100}
	for i, b := range []Blend{BlendMultiply, BlendNormal, BlendMultiply} {
		if err := d.StampImage(i+1, grey, r, b); err != nil {
			t.Fatal(err)
		}
	}
	out := writeBytes(t, d)
	orig := readValidated(t, bg, nil)
	ctx := readValidated(t, out, nil)
	for _, n := range []int{1, 3} {
		last := checkStamped(t, orig, ctx, n, 1)
		if !strings.Contains(last, "/RmGS0 gs") {
			t.Fatalf("page %d: no gs in %q", n, last)
		}
		_, attrs := pageDict(t, ctx, n)
		gs, err := ctx.DereferenceDict(resourceEntry(t, ctx, attrs.Resources, "ExtGState", "RmGS0"))
		if err != nil {
			t.Fatal(err)
		}
		if bm := gs.NameEntry("BM"); bm == nil || *bm != "Multiply" {
			t.Errorf("page %d: ExtGState %v", n, gs)
		}
	}
	// A grey image is encoded as DeviceGray without SMask.
	_, attrs := pageDict(t, ctx, 2)
	sd, _, _ := ctx.DereferenceStreamDict(resourceEntry(t, ctx, attrs.Resources, "XObject", "RmInk0"))
	if cs := sd.NameEntry("ColorSpace"); cs == nil || *cs != "DeviceGray" || sd.Dict["SMask"] != nil {
		t.Errorf("grey opaque image: %v", sd.Dict)
	}

	if !havePoppler() {
		t.Log("pdftoppm not on PATH; render check skipped")
		return
	}
	px := func(n int) color.Color { return render(t, out, n).At(100, 100) }
	r1, g1, b1, _ := px(1).RGBA()
	if r1>>8 > 10 || b1>>8 > 10 || g1>>8 < 118 || g1>>8 > 138 {
		t.Errorf("multiply over green: got rgb(%d,%d,%d), want ~(0,128,0)", r1>>8, g1>>8, b1>>8)
	}
	r2, g2, b2, _ := px(2).RGBA()
	if r2>>8 < 118 || r2>>8 > 138 || g2>>8 != r2>>8 || b2>>8 != r2>>8 {
		t.Errorf("normal over green: got rgb(%d,%d,%d), want ~(128,128,128)", r2>>8, g2>>8, b2>>8)
	}
	r3, g3, b3, _ := px(3).RGBA()
	if r3>>8 > 10 || b3>>8 > 10 || g3>>8 < 118 || g3>>8 > 138 {
		t.Errorf("multiply rot90: got rgb(%d,%d,%d)", r3>>8, g3>>8, b3>>8)
	}
}

// TestStampContent draws a 40x40 square at the visual bottom-left in visual
// PDF user space, once with a multiply ExtGState from Resources.
func TestStampContent(t *testing.T) {
	pages := []pageSpec{
		{MediaBox: [4]float64{0, 0, 200, 300}, Annots: true},
		{MediaBox: [4]float64{100, 50, 300, 350}},
		{MediaBox: [4]float64{10, 20, 410, 320}, CropBox: box(60, 40, 360, 240), Rotate: 90},
		{MediaBox: [4]float64{30, 40, 230, 340}, Rotate: 180, Annots: true},
		{MediaBox: [4]float64{0, 0, 300, 200}, Rotate: 270},
		{MediaBox: [4]float64{0, 0, 200, 300}},
	}
	wantCM := []matrix{
		{1, 0, 0, 1, 0, 0},
		{1, 0, 0, 1, 100, 50},
		{0, 1, -1, 0, 360, 40},
		{-1, 0, 0, -1, 230, 340},
		{0, -1, 1, 0, 0, 200},
		{1, 0, 0, 1, 0, 0},
	}
	bg := backgroundPDF(pages, "")
	d := openBytes(t, bg)
	plain := []byte("1 0 0 rg 0 0 40 40 re f")
	mult := []byte("/GS0 gs 1 0 0 rg 0 0 40 40 re f")
	res := Resources{ExtGState: map[string]map[string]any{"GS0": {"BM": "Multiply", "ca": 1.0}}}
	for i := range pages {
		c, r := plain, Resources{}
		if i == len(pages)-1 {
			c, r = mult, res
		}
		if err := d.StampContent(i+1, c, r); err != nil {
			t.Fatal(err)
		}
	}
	out := writeBytes(t, d)
	orig := readValidated(t, bg, nil)
	ctx := readValidated(t, out, nil)
	for i := range pages {
		last := checkStamped(t, orig, ctx, i+1, 1)
		if got := cmOf(t, last); !sameMatrix(got, wantCM[i]) {
			t.Errorf("page %d: cm %v, want %v", i+1, got, wantCM[i])
		}
		_, attrs := pageDict(t, ctx, i+1)
		form, _, err := ctx.DereferenceStreamDict(resourceEntry(t, ctx, attrs.Resources, "XObject", xobjectName(t, last)))
		if err != nil || form == nil || *form.Subtype() != "Form" {
			t.Fatalf("page %d: form xobject: %v", i+1, err)
		}
		vw, vh, _ := d.PageSize(i + 1)
		if got, want := entry(form.Dict, "BBox"), types.NewNumberArray(0, 0, vw, vh).PDFString(); got != want {
			t.Errorf("page %d: BBox %s, want %s", i+1, got, want)
		}
		if body := decodeStream(t, ctx, resourceEntry(t, ctx, attrs.Resources, "XObject", xobjectName(t, last))); body != string(map[bool][]byte{true: mult, false: plain}[i == len(pages)-1]) {
			t.Errorf("page %d: form content %q", i+1, body)
		}
	}
	_, attrs := pageDict(t, ctx, len(pages))
	form, _, _ := ctx.DereferenceStreamDict(resourceEntry(t, ctx, attrs.Resources, "XObject", "RmInk0"))
	fres, _ := ctx.DereferenceDict(form.Dict["Resources"])
	gs, _ := ctx.DereferenceDict(resourceEntry(t, ctx, fres, "ExtGState", "GS0"))
	if bm := gs.NameEntry("BM"); bm == nil || *bm != "Multiply" {
		t.Errorf("form ExtGState GS0 = %v", gs)
	}
	if hasResource(ctx, attrs.Resources, "ExtGState", "GS0") {
		t.Errorf("caller resource names must not leak into the page resources")
	}

	if !havePoppler() {
		t.Log("pdftoppm not on PATH; render check skipped")
		return
	}
	for i := range pages {
		img := render(t, out, i+1)
		h := imgRect(img).Dy()
		want := "red"
		if i == len(pages)-1 {
			want = "black" // red multiplied onto green
		}
		if got := classify(img.At(10, h-10)); got != want {
			t.Errorf("page %d: visual bottom-left %s, want %s", i+1, got, want)
		}
		if got := extent(img, want); !near(got, image.Rect(0, h-40, 40, h), 1) {
			t.Errorf("page %d: %s extent %v", i+1, want, got)
		}
		if got := classify(img.At(imgRect(img).Dx()-10, 10)); got != "green" {
			t.Errorf("page %d: visual top-right %s, want green", i+1, got)
		}
	}
}

// TestStampResourceCollisions: existing RmInk0/RmGS0 names are kept, new ones
// are chosen around them, and inherited or shared Resources dicts are
// copied rather than modified.
func TestStampResourceCollisions(t *testing.T) {
	form := stream("")
	form = strings.Replace(form, "<< /Length 0 >>", "<< /Type /XObject /Subtype /Form /BBox [0 0 1 1] /Length 0 >>", 1)
	pages := []pageSpec{
		{MediaBox: [4]float64{0, 0, 200, 300}, Resources: "<< /XObject << /RmInk0 13 0 R >> /ExtGState << /RmGS0 14 0 R >> >>"},
		{MediaBox: [4]float64{0, 0, 200, 300}, Resources: "-"},      // inherits from the root
		{MediaBox: [4]float64{0, 0, 200, 300}, Resources: "-"},      // inherits from the root
		{MediaBox: [4]float64{0, 0, 200, 300}, Resources: "15 0 R"}, // shared
		{MediaBox: [4]float64{0, 0, 200, 300}, Resources: "15 0 R"}, // shared
	}
	// Objects: catalog 1, root 2, pages 3..12 (content, page each), then
	// 13 = form XObject, 14 = ExtGState, 15 = shared Resources dict.
	bg := backgroundPDF(pages, " /Resources << /XObject << /RmInk0 13 0 R >> >>",
		form, "<< /Type /ExtGState /CA 1 >>", "<< /ExtGState << /RmGS0 14 0 R >> >>")
	d := openBytes(t, bg)
	img := quadrantImage(40, 60)
	for _, n := range []int{1, 2, 4} {
		if err := d.StampImage(n, img, Rect{0, 0, 200, 300}, BlendMultiply); err != nil {
			t.Fatal(err)
		}
	}
	out := writeBytes(t, d)
	orig := readValidated(t, bg, nil)
	ctx := readValidated(t, out, nil)

	check := func(n int, wantX, wantGS string, keep ...string) {
		t.Helper()
		last := checkStamped(t, orig, ctx, n, 1)
		if got := xobjectName(t, last); got != wantX {
			t.Errorf("page %d: xobject %s, want %s", n, got, wantX)
		}
		if !strings.Contains(last, "/"+wantGS+" gs") {
			t.Errorf("page %d: want /%s gs in %q", n, wantGS, last)
		}
		_, attrs := pageDict(t, ctx, n)
		for _, k := range keep {
			kind, name, _ := strings.Cut(k, ":")
			if !hasResource(ctx, attrs.Resources, kind, name) {
				t.Errorf("page %d: lost existing resource %s", n, k)
			}
		}
	}
	check(1, "RmInk1", "RmGS1", "XObject:RmInk0", "ExtGState:RmGS0")
	check(2, "RmInk1", "RmGS0", "XObject:RmInk0")
	check(4, "RmInk0", "RmGS1", "ExtGState:RmGS0")

	// Unstamped pages still see the original (inherited / shared) resources.
	for _, n := range []int{3, 5} {
		od, oattrs := pageDict(t, orig, n)
		nd, nattrs := pageDict(t, ctx, n)
		if entry(od, "Resources") != entry(nd, "Resources") || len(contents(t, ctx, nd)) != 1 {
			t.Errorf("page %d modified", n)
		}
		if oattrs.Resources.PDFString() != nattrs.Resources.PDFString() {
			t.Errorf("page %d: effective resources changed: %s -> %s", n, oattrs.Resources.PDFString(), nattrs.Resources.PDFString())
		}
	}
	root, _ := ctx.DereferenceDict(*mustPages(t, ctx))
	if hasResource(ctx, root.DictEntry("Resources"), "XObject", "RmInk1") {
		t.Errorf("root resources modified")
	}
}

func mustPages(t testing.TB, ctx interface {
	Pages() (*types.IndirectRef, error)
}) *types.IndirectRef {
	t.Helper()
	r, err := ctx.Pages()
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// TestStampTwice: two stamps on one page nest cleanly.
func TestStampTwice(t *testing.T) {
	bg := backgroundPDF([]pageSpec{{MediaBox: [4]float64{0, 0, 200, 300}, Rotate: 90}}, "")
	d := openBytes(t, bg)
	if err := d.StampImage(1, quadrantImage(40, 60), Rect{0, 0, 200, 300}, BlendMultiply); err != nil {
		t.Fatal(err)
	}
	if err := d.StampContent(1, []byte("0 0 1 rg 0 0 10 10 re f"), Resources{}); err != nil {
		t.Fatal(err)
	}
	out := writeBytes(t, d)
	orig := readValidated(t, bg, nil)
	ctx := readValidated(t, out, nil)
	nd, _ := pageDict(t, ctx, 1)
	nc := contents(t, ctx, nd)
	if len(nc) != 5 { // q q orig Q-stamp1 Q-stamp2
		t.Fatalf("%d streams", len(nc))
	}
	if !strings.Contains(nc[3], "/RmInk0 Do") || !strings.Contains(nc[4], "/RmInk1 Do") {
		t.Errorf("stamp streams: %q %q", nc[3], nc[4])
	}
	od, _ := pageDict(t, orig, 1)
	for _, k := range []string{"MediaBox", "CropBox", "Rotate"} {
		if entry(od, k) != entry(nd, k) {
			t.Errorf("/%s changed", k)
		}
	}
}

func TestStampErrors(t *testing.T) {
	d := openBytes(t, backgroundPDF([]pageSpec{{MediaBox: [4]float64{0, 0, 200, 300}}}, ""))
	img := quadrantImage(4, 4)
	for name, err := range map[string]error{
		"page 0":       d.StampImage(0, img, Rect{0, 0, 1, 1}, BlendNormal),
		"page 2":       d.StampImage(2, img, Rect{0, 0, 1, 1}, BlendNormal),
		"zero rect":    d.StampImage(1, img, Rect{0, 0, 0, 1}, BlendNormal),
		"nil image":    d.StampImage(1, nil, Rect{0, 0, 1, 1}, BlendNormal),
		"empty image":  d.StampImage(1, image.NewNRGBA(image.Rect(0, 0, 0, 0)), Rect{0, 0, 1, 1}, BlendNormal),
		"content page": d.StampContent(3, nil, Resources{}),
		"bad resource": d.StampContent(1, nil, Resources{ExtGState: map[string]map[string]any{"G": {"x": struct{}{}}}}),
	} {
		if err == nil {
			t.Errorf("%s: want error", name)
		}
	}
	if _, _, err := d.PageSize(9); err == nil {
		t.Errorf("PageSize(9): want error")
	}
}
