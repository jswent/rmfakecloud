package inkpdf

// Test fixtures, ported from the pdfcpu research (scratchpad/research/pdfcpu/fixtures):
// quadrant images with alpha, hand-written background PDFs with odd page boxes
// and /Rotate, and a poppler-based renderer with pixel probes.

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var (
	red   = color.NRGBA{255, 0, 0, 255}
	blue  = color.NRGBA{0, 0, 255, 255}
	clear = color.NRGBA{0, 0, 0, 0}
)

// quadrantImage: top-left quadrant opaque red, bottom-right opaque blue, the
// other two fully transparent.
func quadrantImage(w, h int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := clear
			if x < w/2 && y < h/2 {
				c = red
			} else if x >= w/2 && y >= h/2 {
				c = blue
			}
			img.SetNRGBA(x, y, c)
		}
	}
	return img
}

func solidImage(w, h int, c color.NRGBA) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = c.R, c.G, c.B, c.A
	}
	return img
}

// pdfBuilder writes a classic-xref PDF from hand-written objects.
type pdfBuilder struct{ objs []string }

// add reserves the next object number and returns it.
func (b *pdfBuilder) add(s string) int { b.objs = append(b.objs, s); return len(b.objs) }

func (b *pdfBuilder) set(n int, s string) { b.objs[n-1] = s }

func (b *pdfBuilder) bytes(root int) []byte {
	var out bytes.Buffer
	out.WriteString("%PDF-1.7\n%\xe2\xe3\xcf\xd3\n")
	offs := make([]int, len(b.objs))
	for i, o := range b.objs {
		offs[i] = out.Len()
		fmt.Fprintf(&out, "%d 0 obj\n%s\nendobj\n", i+1, o)
	}
	xref := out.Len()
	fmt.Fprintf(&out, "xref\n0 %d\n0000000000 65535 f \n", len(b.objs)+1)
	for _, o := range offs {
		fmt.Fprintf(&out, "%010d 00000 n \n", o)
	}
	fmt.Fprintf(&out, "trailer\n<< /Size %d /Root %d 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(b.objs)+1, root, xref)
	return out.Bytes()
}

func stream(content string) string {
	return fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content)
}

// pageSpec describes one background page. Every page is filled solid green
// over its MediaBox.
type pageSpec struct {
	MediaBox  [4]float64
	CropBox   *[4]float64
	Rotate    int
	Resources string // page /Resources value; "" means "<< >>", "-" omits the entry
	Annots    bool   // add an invisible /Link annotation
	Extra     string // extra content operators
}

func box(a, b, c, d float64) *[4]float64 { return &[4]float64{a, b, c, d} }

// backgroundPDF builds a flat page tree (catalog 1, pages 2). rootExtra is
// appended to the root /Pages dict (for inherited attributes); extraObjs are
// added after the pages and may be referenced by number from the specs.
func backgroundPDF(pages []pageSpec, rootExtra string, extraObjs ...string) []byte {
	b := &pdfBuilder{}
	b.add("<< /Type /Catalog /Pages 2 0 R >>")
	b.add("") // pages root, set below
	kids := make([]string, len(pages))
	for i, p := range pages {
		mb := p.MediaBox
		content := fmt.Sprintf("0 1 0 rg %g %g %g %g re f", mb[0], mb[1], mb[2]-mb[0], mb[3]-mb[1]) + p.Extra
		c := b.add(stream(content))
		var extra strings.Builder
		if p.CropBox != nil {
			cb := *p.CropBox
			fmt.Fprintf(&extra, " /CropBox [%g %g %g %g]", cb[0], cb[1], cb[2], cb[3])
		}
		if p.Rotate != 0 {
			fmt.Fprintf(&extra, " /Rotate %d", p.Rotate)
		}
		switch p.Resources {
		case "":
			extra.WriteString(" /Resources << >>")
		case "-":
		default:
			extra.WriteString(" /Resources " + p.Resources)
		}
		if p.Annots {
			fmt.Fprintf(&extra, " /Annots [<< /Type /Annot /Subtype /Link /Rect [%g %g %g %g] /Border [0 0 0] /A << /S /URI /URI (https://example.com/%d) >> >>]",
				mb[0]+10, mb[1]+10, mb[0]+60, mb[1]+40, i+1)
		}
		n := b.add(fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [%g %g %g %g]%s /Contents %d 0 R >>",
			mb[0], mb[1], mb[2], mb[3], extra.String(), c))
		kids[i] = fmt.Sprintf("%d 0 R", n)
	}
	for _, o := range extraObjs {
		b.add(o)
	}
	b.set(2, fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d%s >>", strings.Join(kids, " "), len(pages), rootExtra))
	return b.bytes(1)
}

// havePoppler reports whether pdftoppm is on PATH.
func havePoppler() bool {
	_, err := exec.LookPath("pdftoppm")
	return err == nil
}

func needPoppler(t testing.TB) {
	t.Helper()
	if !havePoppler() {
		t.Skip("pdftoppm (poppler) not on PATH; skipping render check")
	}
}

// render renders page n of pdf at 72 dpi with poppler (-cropbox), so one
// pixel is one point of the visual page.
func render(t testing.TB, pdf []byte, n int) image.Image {
	t.Helper()
	dir := t.TempDir()
	in := filepath.Join(dir, "in.pdf")
	if err := os.WriteFile(in, pdf, 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out")
	cmd := exec.Command("pdftoppm", "-cropbox", "-r", "72", "-f", fmt.Sprint(n), "-l", fmt.Sprint(n), "-singlefile", "-png", in, out)
	if o, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("pdftoppm: %v %s", err, o)
	}
	f, err := os.ReadFile(out + ".png")
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(f))
	if err != nil {
		t.Fatal(err)
	}
	return img
}

// classify names the dominant colour of a pixel.
func classify(c color.Color) string {
	r, g, b, _ := c.RGBA()
	r, g, b = r>>8, g>>8, b>>8
	switch {
	case r > 200 && g < 60 && b < 60:
		return "red"
	case b > 200 && r < 60 && g < 60:
		return "blue"
	case g > 200 && r < 60 && b < 60:
		return "green"
	case r > 200 && g > 200 && b > 200:
		return "white"
	case r < 60 && g < 60 && b < 60:
		return "black"
	}
	return fmt.Sprintf("rgb(%d,%d,%d)", r, g, b)
}

// quadrants samples the visual page at the centres of its quadrants.
func quadrants(img image.Image) string {
	bd := img.Bounds()
	w, h := bd.Dx(), bd.Dy()
	pt := func(fx, fy float64) string {
		return classify(img.At(bd.Min.X+int(fx*float64(w)), bd.Min.Y+int(fy*float64(h))))
	}
	return fmt.Sprintf("%dx%d TL=%s TR=%s BL=%s BR=%s", w, h, pt(.25, .25), pt(.75, .25), pt(.25, .75), pt(.75, .75))
}

// extent returns the bounding box of pixels of the named colour.
func extent(img image.Image, name string) image.Rectangle {
	bd := img.Bounds()
	r := image.Rectangle{}
	for y := bd.Min.Y; y < bd.Max.Y; y++ {
		for x := bd.Min.X; x < bd.Max.X; x++ {
			if classify(img.At(x, y)) == name {
				r = r.Union(image.Rect(x, y, x+1, y+1))
			}
		}
	}
	return r.Sub(bd.Min)
}

// diff counts pixels whose colour class differs (-1 if sizes differ).
func diff(a, b image.Image) int {
	if a.Bounds().Size() != b.Bounds().Size() {
		return -1
	}
	n := 0
	for y := 0; y < a.Bounds().Dy(); y++ {
		for x := 0; x < a.Bounds().Dx(); x++ {
			if classify(a.At(a.Bounds().Min.X+x, a.Bounds().Min.Y+y)) != classify(b.At(b.Bounds().Min.X+x, b.Bounds().Min.Y+y)) {
				n++
			}
		}
	}
	return n
}

// near reports whether two rectangles match within tol pixels on every edge.
func near(a, b image.Rectangle, tol int) bool {
	abs := func(v int) int {
		if v < 0 {
			return -v
		}
		return v
	}
	return abs(a.Min.X-b.Min.X) <= tol && abs(a.Min.Y-b.Min.Y) <= tol && abs(a.Max.X-b.Max.X) <= tol && abs(a.Max.Y-b.Max.Y) <= tol
}
