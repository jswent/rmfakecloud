package inkpdf

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"math"
	"strings"
	"testing"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

// rM1/rM2 page at 72/226 pt per page unit, and a page that grew downwards.
const (
	rmW  = 1404 * 72.0 / 226
	rmH  = 1872 * 72.0 / 226
	rmH2 = 3000 * 72.0 / 226
)

func TestAddImagePageSizes(t *testing.T) {
	d, err := NewDocument()
	if err != nil {
		t.Fatal(err)
	}
	sizes := [][2]float64{{rmW, rmH}, {rmW, rmH2}}
	imgs := []*image.NRGBA{quadrantImage(1404, 1872), quadrantImage(1404, 3000)}
	for i, s := range sizes {
		n, err := d.AddImagePage(imgs[i], s[0], s[1])
		if err != nil || n != i+1 {
			t.Fatalf("AddImagePage: %d %v", n, err)
		}
		w, h, err := d.PageSize(n)
		if err != nil || w != s[0] || h != s[1] {
			t.Errorf("PageSize(%d) = %vx%v %v, want %vx%v", n, w, h, err, s[0], s[1])
		}
	}
	if n, err := d.AddBlankPage(200, 300); err != nil || n != 3 {
		t.Fatalf("AddBlankPage: %d %v", n, err)
	}
	if d.PageCount() != 3 {
		t.Fatalf("PageCount %d", d.PageCount())
	}
	out := writeBytes(t, d)
	ctx := readValidated(t, out, nil)
	if ctx.PageCount != 3 {
		t.Fatalf("page count %d", ctx.PageCount)
	}
	for i, s := range append(sizes, [2]float64{200, 300}) {
		pd, _ := pageDict(t, ctx, i+1)
		want := types.NewNumberArray(0, 0, s[0], s[1]).PDFString()
		if got := entry(pd, "MediaBox"); got != want {
			t.Errorf("page %d MediaBox %s, want %s", i+1, got, want)
		}
		if got := entry(pd, "CropBox"); got != want {
			t.Errorf("page %d CropBox %s, want %s", i+1, got, want)
		}
		if got := entry(pd, "Rotate"); got != "0" {
			t.Errorf("page %d Rotate %q, want explicit 0", i+1, got)
		}
		if i < 2 {
			c := contents(t, ctx, pd)
			if len(c) != 1 || !sameMatrix(cmOf(t, c[0]), matrix{s[0], 0, 0, s[1], 0, 0}) {
				t.Errorf("page %d content %q", i+1, c)
			}
		}
	}
	checkDepth1(t, ctx, 3)

	if !havePoppler() {
		t.Log("pdftoppm not on PATH; render check skipped")
		return
	}
	// poppler rounds the 447.29 x 596.39 pt page up to whole pixels.
	for i, want := range []string{"448x597 TL=red TR=white BL=white BR=blue", "448x956 TL=red TR=white BL=white BR=blue"} {
		if got := quadrants(render(t, out, i+1)); got != want {
			t.Errorf("page %d: %s, want %s", i+1, got, want)
		}
	}
	blank := render(t, out, 3)
	if sz := imgRect(blank).Size(); sz != image.Pt(200, 300) {
		t.Errorf("blank size %v", sz)
	}
	if n := len(extentNot(blank, "white")); n != 0 {
		t.Errorf("blank page has %d non-white pixels", n)
	}
}

func extentNot(img image.Image, name string) []image.Point {
	var pts []image.Point
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if classify(img.At(x, y)) != name {
				pts = append(pts, image.Pt(x, y))
			}
		}
	}
	return pts
}

// checkDepth1 walks the page tree and requires every kid of the root to be
// a leaf page.
func checkDepth1(t testing.TB, ctx *model.Context, wantPages int) {
	t.Helper()
	rootRef := mustPages(t, ctx)
	root, err := ctx.DereferenceDict(*rootRef)
	if err != nil {
		t.Fatal(err)
	}
	kids, err := ctx.DereferenceArray(root["Kids"])
	if err != nil {
		t.Fatal(err)
	}
	if len(kids) != wantPages {
		t.Fatalf("root has %d kids, want %d", len(kids), wantPages)
	}
	if c := root.IntEntry("Count"); c == nil || *c != wantPages {
		t.Errorf("root /Count %v, want %d", c, wantPages)
	}
	for i, k := range kids {
		kd, err := ctx.DereferenceDict(k)
		if err != nil {
			t.Fatal(err)
		}
		if tp := kd.Type(); tp == nil || *tp != "Page" {
			t.Fatalf("kid %d is /%v, want /Page (tree depth > 1)", i, tp)
		}
		if p, ok := kd["Parent"].(types.IndirectRef); !ok || p.ObjectNumber != rootRef.ObjectNumber {
			t.Errorf("kid %d parent %v", i, kd["Parent"])
		}
	}
}

func TestAppendAfterLargeBackground(t *testing.T) {
	specs := make([]pageSpec, 150)
	for i := range specs {
		specs[i] = pageSpec{MediaBox: [4]float64{0, 0, 612, 792}}
	}
	// An inheritable CropBox and Rotate on the root must not reach new pages.
	bg := backgroundPDF(specs, " /CropBox [0 0 100 100] /Rotate 90")
	d := openBytes(t, bg)
	ink := quadrantImage(140, 187)
	for i := 0; i < 48; i++ {
		if _, err := d.AddImagePage(ink, rmW, rmH); err != nil {
			t.Fatal(err)
		}
	}
	n, err := d.AddBlankPage(rmW, rmH)
	if err != nil || n != 199 {
		t.Fatalf("AddBlankPage %d %v", n, err)
	}
	if err := d.StampImage(199, ink, Rect{0, 0, rmW, rmH}, BlendNormal); err != nil {
		t.Fatal(err)
	}
	if w, h, _ := d.PageSize(199); w != rmW || h != rmH {
		t.Errorf("appended PageSize %vx%v", w, h)
	}
	out := writeBytes(t, d)
	ctx := readValidated(t, out, nil)
	if ctx.PageCount != 199 {
		t.Fatalf("page count %d", ctx.PageCount)
	}
	checkDepth1(t, ctx, 199)
	_, attrs := pageDict(t, ctx, 170)
	if attrs.Rotate != 0 || math.Abs(attrs.CropBox.Width()-rmW) > 1e-6 {
		t.Errorf("appended page inherited root attributes: rot %d crop %v", attrs.Rotate, attrs.CropBox)
	}
	if havePoppler() {
		if got := quadrants(render(t, out, 199)); got != "448x597 TL=red TR=white BL=white BR=blue" {
			t.Errorf("stamped blank page: %s", got)
		}
	}
}

func encrypt(t testing.TB, pdf []byte, user, owner string) []byte {
	t.Helper()
	conf := model.NewAESConfiguration(user, owner, 256)
	conf.ValidationMode = model.ValidationRelaxed
	var b bytes.Buffer
	if err := api.Encrypt(context.Background(), bytes.NewReader(pdf), &b, conf); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestEncryptedInput(t *testing.T) {
	plain := backgroundPDF([]pageSpec{{MediaBox: [4]float64{0, 0, 200, 300}}, {MediaBox: [4]float64{0, 0, 300, 200}, Rotate: 90}}, "")
	userEnc := encrypt(t, plain, "secret", "owner")
	ownerOnly := encrypt(t, plain, "", "owner")

	for name, opts := range map[string]OpenOptions{
		"no password":    {Decrypt: true},
		"wrong password": {UserPassword: "nope", Decrypt: true},
		"wrong, keep":    {UserPassword: "nope"},
	} {
		_, err := Open(bytes.NewReader(userEnc), opts)
		if !errors.Is(err, pdfcpu.ErrWrongPassword) || !errors.Is(err, ErrWrongPassword) {
			t.Errorf("%s: err = %v, want ErrWrongPassword", name, err)
		}
	}

	stampWrite := func(pdf []byte, opts OpenOptions) []byte {
		t.Helper()
		d, err := Open(bytes.NewReader(pdf), opts)
		if err != nil {
			t.Fatalf("Open %+v: %v", opts, err)
		}
		if err := d.StampImage(2, quadrantImage(40, 60), Rect{0, 0, 200, 300}, BlendMultiply); err != nil {
			t.Fatal(err)
		}
		return writeBytes(t, d)
	}
	isEncrypted := func(pdf []byte) bool {
		ctx, err := api.ReadContext(context.Background(), bytes.NewReader(pdf), model.NewStatelessConfiguration())
		if errors.Is(err, pdfcpu.ErrWrongPassword) {
			return true
		}
		if err != nil {
			t.Fatal(err)
		}
		return ctx.Encrypt != nil
	}

	// Decrypt: output is plain, valid, and stamped.
	for name, opts := range map[string]OpenOptions{
		"user password": {UserPassword: "secret", Decrypt: true},
		"owner only":    {Decrypt: true},
		"owner pw":      {OwnerPassword: "owner", Decrypt: true},
	} {
		src := userEnc
		if name == "owner only" {
			src = ownerOnly
		}
		out := stampWrite(src, opts)
		if isEncrypted(out) {
			t.Errorf("%s: output still encrypted", name)
			continue
		}
		ctx := readValidated(t, out, nil)
		nd, _ := pageDict(t, ctx, 2)
		if c := contents(t, ctx, nd); !strings.Contains(c[len(c)-1], "/RmGS0 gs") {
			t.Errorf("%s: page not stamped", name)
		}
		if havePoppler() { // multiply: red and blue over green both give black
			if got := quadrants(render(t, out, 2)); !strings.HasSuffix(got, "TL=black TR=green BL=green BR=black") {
				t.Errorf("%s: render %s", name, got)
			}
		}
	}

	// Without Decrypt the output stays encrypted with the same password.
	out := stampWrite(userEnc, OpenOptions{UserPassword: "secret"})
	if !isEncrypted(out) {
		t.Errorf("output not re-encrypted")
	}
	conf := model.NewStatelessConfiguration()
	conf.UserPW = "secret"
	readValidated(t, out, conf)
}

func TestOpenInvalid(t *testing.T) {
	for name, in := range map[string][]byte{"empty": nil, "not a pdf": []byte("hello world")} {
		if _, err := Open(bytes.NewReader(in), OpenOptions{}); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
	d, _ := NewDocument()
	for _, s := range [][2]float64{{0, 100}, {100, -1}, {100, 20000}} {
		if _, err := d.AddBlankPage(s[0], s[1]); err == nil {
			t.Errorf("AddBlankPage(%v): want error", s)
		}
	}
	if _, err := d.AddPreparedPage(nil, 100, 100); err == nil {
		t.Errorf("AddPreparedPage(nil): want error")
	}
}

func TestPrepareImage(t *testing.T) {
	opaqueRGB := solidImage(8, 4, color.NRGBA{10, 20, 30, 255})
	// Grey where visible; the colour under alpha 0 does not count.
	grey := solidImage(8, 4, color.NRGBA{50, 50, 50, 128})
	grey.SetNRGBA(1, 1, color.NRGBA{255, 0, 0, 0})
	sub := quadrantImage(20, 20).SubImage(image.Rect(10, 10, 20, 20)).(*image.NRGBA) // all blue

	cases := []struct {
		name      string
		img       *image.NRGBA
		gray, smk bool
	}{
		{"opaque rgb", opaqueRGB, false, false},
		{"grey with alpha", grey, true, true},
		{"quadrants", quadrantImage(8, 8), false, true},
		{"sub image", sub, false, false},
	}
	imgs := make([]*image.NRGBA, len(cases))
	for i, c := range cases {
		imgs[i] = c.img
	}
	preps, err := PrepareImages(imgs, 3)
	if err != nil {
		t.Fatal(err)
	}
	d, _ := NewDocument()
	for i, c := range cases {
		p := preps[i]
		if p.Width() != c.img.Rect.Dx() || p.Height() != c.img.Rect.Dy() || p.gray != c.gray || (p.alpha != nil) != c.smk || p.EncodedSize() == 0 {
			t.Errorf("%s: %dx%d gray=%v smask=%v", c.name, p.Width(), p.Height(), p.gray, p.alpha != nil)
		}
		if _, err := d.AddPreparedPage(p, 100, 100); err != nil {
			t.Fatal(err)
		}
	}
	out := writeBytes(t, d)
	ctx := readValidated(t, out, nil)
	// Decode the sub image back: 10x10 opaque blue, RGB.
	_, attrs := pageDict(t, ctx, 4)
	sd, _, _ := ctx.DereferenceStreamDict(resourceEntry(t, ctx, attrs.Resources, "XObject", "RmInk0"))
	if err := sd.Decode(); err != nil {
		t.Fatal(err)
	}
	if len(sd.Content) != 300 || !bytes.Equal(sd.Content[:3], []byte{0, 0, 255}) || !bytes.Equal(sd.Content[297:], []byte{0, 0, 255}) {
		t.Errorf("sub image pixels: len %d %v", len(sd.Content), sd.Content[:3])
	}
	if havePoppler() {
		if got := quadrants(render(t, out, 3)); got != "100x100 TL=red TR=white BL=white BR=blue" {
			t.Errorf("quadrant page: %s", got)
		}
	}

	if _, err := PrepareImages([]*image.NRGBA{opaqueRGB, image.NewNRGBA(image.Rect(0, 0, 0, 3))}, 2); err == nil {
		t.Errorf("PrepareImages with empty image: want error")
	}
	if p, err := PrepareImages([]*image.NRGBA{nil}, 0); err != nil || p[0] != nil {
		t.Errorf("nil entry: %v %v", p, err)
	}
}

// TestPreparedNoDecodedCopy: inserted images keep only the compressed bytes.
func TestPreparedNoDecodedCopy(t *testing.T) {
	d := openBytes(t, backgroundPDF([]pageSpec{{MediaBox: [4]float64{0, 0, 200, 300}}}, ""))
	if err := d.StampImage(1, quadrantImage(400, 600), Rect{0, 0, 200, 300}, BlendNormal); err != nil {
		t.Fatal(err)
	}
	for nr, e := range d.ctx.Table {
		if sd, ok := e.Object.(types.StreamDict); ok && sd.Subtype() != nil && *sd.Subtype() == "Image" {
			if sd.Content != nil {
				t.Errorf("obj %d: decoded image kept in xref (%d bytes)", nr, len(sd.Content))
			}
			if len(sd.Raw) == 0 {
				t.Errorf("obj %d: no raw stream", nr)
			}
		}
	}
}
