//go:build cgo && rmlines

package exporter

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/ddvk/rmfakecloud/internal/storage/exporter/inkpdf"
	"github.com/ddvk/rmfakecloud/internal/storage/exporter/templates"
	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
)

// outDir is where the tagged tests leave their exports for inspection.
func outDir(t testing.TB) string {
	dir := os.Getenv("RMEXPORT_OUT_DIR")
	if dir == "" {
		dir = filepath.Join(os.TempDir(), "rmexport-out")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// v6 fixtures in a synthetic notebook and on a synthetic PDF.
func TestExportV6Fixtures(t *testing.T) {
	src := &Source{
		Content: []byte(`{"fileType":"notebook","formatVersion":1,"pages":["tools","down","empty","v5"]}`),
		Pages: map[string]Blob{
			"tools": {Path: filepath.Join(fixtures, "v6/Test_all_tools.rm")},
			"down":  {Data: fixture(t, "v6/Page_down_test.rm")}, // written to a temp file
			"v5":    {Data: fixture(t, "v5/test_v5.rm")},
		},
	}
	pdf, st := export(t, Config{}, src)
	os.WriteFile(filepath.Join(outDir(t), "v6-fixtures-notebook.pdf"), pdf, 0o644)
	pages := readPDF(t, pdf)
	if len(pages) != 4 || st.V6 != 2 || st.V5 != 1 || st.Blank != 1 || st.Failed != 0 {
		t.Fatalf("%d pages, stats %+v", len(pages), *st)
	}
	checkSize(t, 1, pages[0], 445.3, 593.8)
	if pages[1].w != 445.3 || pages[1].h <= 593.8 {
		t.Errorf("grown page is %vx%v, want 445.3 wide and taller than a page", pages[1].w, pages[1].h)
	}
	for _, i := range []int{0, 1} {
		if !strings.Contains(pages[i].content, " Do") {
			t.Errorf("page %d has no ink image", i+1)
		}
	}
	// The blank page follows the size of the last v6 page.
	checkSize(t, 3, pages[2], 445.3, 593.8)

	bg := &Source{
		Content: []byte(`{"fileType":"pdf","formatVersion":1,"pages":["hl"],"redirectionPageMap":[0]}`),
		Payload: bytes.NewReader(backgroundPDF(t, 6)), // page 1: 100x100
		Pages:   map[string]Blob{"hl": {Path: filepath.Join(fixtures, "v6/Wikipedia_highlighted_p1.rm")}},
	}
	pdf, st = export(t, Config{}, bg)
	os.WriteFile(filepath.Join(outDir(t), "v6-fixtures-pdf.pdf"), pdf, 0o644)
	pages = readPDF(t, pdf)
	if len(pages) != 1 || st.V6 != 1 {
		t.Fatalf("%d pages, stats %+v", len(pages), *st)
	}
	checkSize(t, 1, pages[0], 100, 100)
	// The page holds glyph highlights only (no pen ink): one multiplied image.
	if strings.Count(pages[0].content, " Do") != 1 || !strings.Contains(pages[0].content, " gs") {
		t.Errorf("want multiplied highlights stamped, got %q", pages[0].content)
	}
}

// The user's device samples (testdata/rm/device, not committed): page count,
// order and sizes against the tablet's own export, and ink overlap.
type deviceSample struct {
	name  string
	sizes [][2]float64 // tablet PDF page sizes in pt, in page order
}

var deviceSamples = []deviceSample{
	{"AI training", [][2]float64{{514, 2007}, {514, 1966}, {514, 685}, {514, 685}, {514, 685}}},
	{"Sites", [][2]float64{{445, 681}, {445, 986}, {445, 594}, {445, 858}, {445, 594},
		{514, 685}, {514, 685}, {514, 1512}, {514, 685}, {514, 929}, {514, 685}, {514, 685}, {514, 1579}, {514, 685}, {514, 685}}},
	{"Move", [][2]float64{{260, 1077}, {260, 463}, {260, 2132}, {260, 463}, {260, 463}}},
}

// deviceSource reads an extracted .rmdoc: <id>.content and <id>/<page>.rm.
func deviceSource(t testing.TB, dir string) *Source {
	t.Helper()
	contents, _ := filepath.Glob(filepath.Join(dir, "*.content"))
	if len(contents) != 1 {
		t.Fatalf("%s: %d .content files", dir, len(contents))
	}
	c, err := os.ReadFile(contents[0])
	if err != nil {
		t.Fatal(err)
	}
	src := &Source{Content: c, Pages: map[string]Blob{}}
	rms, _ := filepath.Glob(filepath.Join(strings.TrimSuffix(contents[0], ".content"), "*.rm"))
	for _, p := range rms {
		src.Pages[strings.TrimSuffix(filepath.Base(p), ".rm")] = Blob{Path: p}
	}
	return src
}

func TestDeviceSamples(t *testing.T) {
	root := filepath.Join(fixtures, "device")
	if _, err := os.Stat(root); err != nil {
		t.Skip("no device samples in testdata/rm/device")
	}
	out := outDir(t)
	e := New(Config{})
	defer e.Close()
	for _, s := range deviceSamples {
		t.Run(s.name, func(t *testing.T) {
			dir := filepath.Join(root, s.name)
			if _, err := os.Stat(dir); err != nil {
				t.Skipf("sample %s missing", s.name)
			}
			var buf bytes.Buffer
			st, err := e.Export(t.Context(), deviceSource(t, dir), &buf)
			if err != nil {
				t.Fatal(err)
			}
			ours := filepath.Join(out, s.name+".pdf")
			if err := os.WriteFile(ours, buf.Bytes(), 0o644); err != nil {
				t.Fatal(err)
			}
			pages := readPDF(t, buf.Bytes())
			if len(pages) != len(s.sizes) || st.Failed != 0 {
				t.Fatalf("%d pages (stats %+v), want %d", len(pages), *st, len(s.sizes))
			}
			for i, p := range pages {
				if math.Abs(p.w-s.sizes[i][0]) > 1 || math.Abs(p.h-s.sizes[i][1]) > 1 {
					t.Errorf("page %d is %vx%v pt, tablet %vx%v", i+1, p.w, p.h, s.sizes[i][0], s.sizes[i][1])
				}
			}
			tablet := filepath.Join(root, s.name+".pdf")
			if _, err := os.Stat(tablet); err != nil {
				t.Skip("tablet export missing; ink not compared")
			}
			if _, err := exec.LookPath("pdftoppm"); err != nil {
				t.Skip("pdftoppm not on PATH; ink not compared")
			}
			theirs := rasterise(t, tablet, len(pages))
			mine := rasterise(t, ours, len(pages))
			for i := range pages {
				c := inkCoverage(theirs[i], mine[i])
				t.Logf("%s page %d: %v", s.name, i+1, c)
				if c.inked < 0.85 {
					t.Errorf("page %d: only %.1f%% of the tablet's ink is inked in ours", i+1, c.inked*100)
				}
				if c.precis < 0.85 {
					t.Errorf("page %d: only %.1f%% of our ink lies on the tablet's ink", i+1, c.precis*100)
				}
			}
		})
	}
	t.Logf("exports written to %s", out)
}

// gray is an 8-bit greyscale page.
type gray struct {
	w, h int
	pix  []byte
}

// rasterise renders every page of pdf at 72 dpi in greyscale.
func rasterise(t testing.TB, pdf string, n int) []gray {
	t.Helper()
	dir := t.TempDir()
	cmd := exec.Command("pdftoppm", "-r", "72", "-gray", pdf, filepath.Join(dir, "p"))
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("pdftoppm %s: %v %s", pdf, err, b)
	}
	files, _ := filepath.Glob(filepath.Join(dir, "p-*.pgm"))
	if len(files) != n {
		t.Fatalf("%s: %d pages rendered, want %d", pdf, len(files), n)
	}
	out := make([]gray, n)
	for i, f := range files { // zero-padded names sort in page order
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var w, h, max int
		if _, err := fmt.Sscan(string(b[:min(len(b), 64)]), new(string), &w, &h, &max); err != nil || max != 255 || w*h > len(b) {
			t.Fatalf("%s: not an 8-bit PGM: %v", f, err)
		}
		out[i] = gray{w, h, b[len(b)-w*h:]}
	}
	return out
}

// Grey levels for the ink comparison. The tablet draws its template rules
// at grey 192, so "dark" (< 128) excludes them; "inked" (< 224) is any
// visible ink, for our side, whose strokes are lighter or heavier than the
// tablet's depending on the device (see the report in the test log).
const (
	darkLevel  = 128
	inkedLevel = 224
)

// coverage compares two renders of a page.
type coverage struct {
	exact  float64 // tablet dark pixels that are dark in ours at the same position
	near   float64 // ... dark in ours within 1 px
	inked  float64 // ... inked in ours within 1 px
	precis float64 // our dark pixels that are inked (but not template grey) in the tablet's within 1 px
}

func (c coverage) String() string {
	return fmt.Sprintf("exact %.1f%%, within 1 px %.1f%%, inked within 1 px %.1f%%, our ink on tablet ink %.1f%%",
		c.exact*100, c.near*100, c.inked*100, c.precis*100)
}

func inkCoverage(theirs, ours gray) coverage {
	below := func(g gray, x, y, level int) bool {
		return x >= 0 && y >= 0 && x < g.w && y < g.h && int(g.pix[y*g.w+x]) < level
	}
	around := func(g gray, x, y int, ok func(v int) bool) bool {
		for dy := -1; dy <= 1; dy++ {
			for dx := -1; dx <= 1; dx++ {
				if xx, yy := x+dx, y+dy; xx >= 0 && yy >= 0 && xx < g.w && yy < g.h && ok(int(g.pix[yy*g.w+xx])) {
					return true
				}
			}
		}
		return false
	}
	var total, exact, near, inked int
	for y := 0; y < theirs.h; y++ {
		for x := 0; x < theirs.w; x++ {
			if !below(theirs, x, y, darkLevel) {
				continue
			}
			total++
			if below(ours, x, y, darkLevel) {
				exact++
			}
			if around(ours, x, y, func(v int) bool { return v < darkLevel }) {
				near++
			}
			if around(ours, x, y, func(v int) bool { return v < inkedLevel }) {
				inked++
			}
		}
	}
	var ourDark, onInk int
	for y := 0; y < ours.h; y++ {
		for x := 0; x < ours.w; x++ {
			if !below(ours, x, y, darkLevel) {
				continue
			}
			ourDark++
			if around(theirs, x, y, func(v int) bool { return v < 176 || (v > 208 && v < inkedLevel) }) {
				onInk++
			}
		}
	}
	c := coverage{1, 1, 1, 1}
	if total > 0 {
		c.exact, c.near, c.inked = float64(exact)/float64(total), float64(near)/float64(total), float64(inked)/float64(total)
	}
	if ourDark > 0 {
		c.precis = float64(onInk) / float64(ourDark)
	}
	return c
}

// Blank pages take the size of the document's v6 pages even when they come
// first: a Paper Pro notebook's leading blank page is not rM2-sized. The same
// holds for a password-protected PDF exported as ink only.
func TestBlankPageSizeFromFirstV6(t *testing.T) {
	const ppW, ppH = 513.8, 685.1 // 1620x2160 units at 72/227
	// SceneInfo paper size 1620x2160
	pp := Blob{Path: filepath.Join(fixtures, "v6/test_rmpp_edges.rm")}
	notebook := &Source{
		Content: []byte(`{"fileType":"notebook","formatVersion":1,"pages":["b1","b2","pp","b3"]}`),
		Pages:   map[string]Blob{"pp": pp},
	}
	pdf, st := export(t, Config{}, notebook)
	pages := readPDF(t, pdf)
	if len(pages) != 4 || st.V6 != 1 || st.Blank != 3 {
		t.Fatalf("%d pages, stats %+v", len(pages), *st)
	}
	if !strings.Contains(pages[2].content, " Do") {
		t.Error("the v6 page moved: page 3 has no ink image")
	}
	for _, i := range []int{0, 1, 3} {
		checkSize(t, i+1, pages[i], ppW, ppH)
	}

	locked := &Source{
		Content: []byte(`{"fileType":"pdf","formatVersion":1,"pages":["b1","pp"],"redirectionPageMap":[0,1]}`),
		Payload: bytes.NewReader(encryptPDF(t, backgroundPDF(t, 2), "secret")),
		Pages:   map[string]Blob{"pp": pp},
	}
	pdf, st = export(t, Config{}, locked)
	pages = readPDF(t, pdf)
	if len(pages) != 2 || st.V6 != 1 {
		t.Fatalf("%d pages, stats %+v", len(pages), *st)
	}
	checkSize(t, 1, pages[0], ppW, ppH)
	if pages[1].w != ppW {
		t.Errorf("ink-only page is %v wide, want %v", pages[1].w, ppW)
	}
}

// Notebook pages are one opaque image each (no soft mask, grey when the
// ink is grey), and that image shows the same page as the layered
// construction it replaces: template as vectors, transparent ink over it,
// highlights multiplied on top. The two are rendered with poppler at the
// image's own resolution and compared pixel by pixel.
func TestFlattenedPageMatchesLayered(t *testing.T) {
	e := New(Config{})
	defer e.Close()
	if e.renderer == nil {
		t.Skip("no renderer")
	}
	const tmpl = "P Grid medium"
	src := &Source{
		Content: []byte(`{"fileType":"notebook","formatVersion":2,"orientation":"portrait","pageCount":2,"cPages":{"pages":[` +
			cPageT("hl", "ba", -1, tmpl) + "," + cPageT("tools", "bb", -1, tmpl) + `]}}`),
		Pages: map[string]Blob{
			"hl":    {Path: filepath.Join(fixtures, "v6/Wikipedia_highlighted_p1.rm")},
			"tools": {Path: filepath.Join(fixtures, "v6/Test_all_tools.rm")},
		},
	}
	var flat bytes.Buffer
	st, err := e.Export(t.Context(), src, &flat)
	if err != nil || st.V6 != 2 {
		t.Fatalf("export: %v, stats %+v", err, st)
	}
	os.WriteFile(filepath.Join(outDir(t), "flattened.pdf"), flat.Bytes(), 0o644)
	for i, img := range pageImages(t, flat.Bytes()) {
		if len(img) != 1 || img[0].smask || img[0].cs != "DeviceGray" {
			t.Errorf("page %d images: %+v, want one opaque DeviceGray image", i+1, img)
		}
	}

	// The layered pages, built from the same renders.
	doc, err := inkpdf.NewDocument()
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"hl", "tools"} {
		res, err := e.render(t.Context(), src.Pages[id].Path, false)
		if err != nil {
			t.Fatal(err)
		}
		frame := e.geo.notebookFrame(res)
		r := e.geo.cropRect(res.Ink.Rect, res.Extent, frame)
		ink, hl := crop(res.Ink, r), crop(res.Highlights, r)
		w, h := e.geo.pt(frame.R-frame.L), e.geo.pt(frame.B-frame.T)
		n, err := doc.AddBlankPage(w, h)
		if err != nil {
			t.Fatal(err)
		}
		if c, res, ok := templates.RenderScaled(tmpl, w, h, false, e.geo.ppu); ok {
			if err := doc.StampContent(n, c, res); err != nil {
				t.Fatal(err)
			}
		}
		if hasAlpha(ink) {
			if err := doc.StampImage(n, ink, inkpdf.Rect{W: w, H: h}, inkpdf.BlendNormal); err != nil {
				t.Fatal(err)
			}
		}
		if hl != nil && hasAlpha(hl) {
			if err := doc.StampImage(n, hl, inkpdf.Rect{W: w, H: h}, inkpdf.BlendMultiply); err != nil {
				t.Fatal(err)
			}
		}
	}
	var layered bytes.Buffer
	if err := doc.Write(&layered); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(outDir(t), "layered.pdf"), layered.Bytes(), 0o644)

	if _, err := exec.LookPath("pdftoppm"); err != nil {
		t.Skip("pdftoppm not on PATH; pages not compared")
	}
	dpi := int(math.Round(e.cfg.Scale / e.geo.ppu * 72))
	a := rasteriseRGB(t, filepath.Join(outDir(t), "flattened.pdf"), 2, dpi)
	b := rasteriseRGB(t, filepath.Join(outDir(t), "layered.pdf"), 2, dpi)
	for i := range a {
		if a[i].w != b[i].w || a[i].h != b[i].h {
			t.Fatalf("page %d: %dx%d vs %dx%d", i+1, a[i].w, a[i].h, b[i].w, b[i].h)
		}
		mean, over := nearDiff(a[i], b[i])
		t.Logf("page %d: mean difference %.3f levels, %.4f%% of samples differ by more than 48 (within 1 px)", i+1, mean, 100*over)
		// Poppler snaps the vector rules to whole pixel rows by its own
		// rounding and resamples the ink image slightly differently; both
		// vanish within a pixel (measured: mean 0.3, far under 0.01%). A
		// missing template leaves every rule sample without a match (far
		// 1% and more), and a wrong highlight blend hides or shows text.
		if mean > 0.6 || over > 0.0005 {
			t.Errorf("page %d: flattened page differs from the layered page", i+1)
		}
	}
}

// nearDiff compares two renders of a page, each sample against the best
// matching sample within one pixel in the other, in both directions. It
// returns the mean difference in levels and the fraction of samples that
// differ by more than 48 levels.
func nearDiff(a, b rgb) (mean, over float64) {
	var sum, n, far int
	for _, pair := range [][2]rgb{{a, b}, {b, a}} {
		p, q := pair[0], pair[1]
		for y := 0; y < p.h; y++ {
			for x := 0; x < p.w; x++ {
				for c := 0; c < 3; c++ {
					v := int(p.pix[(y*p.w+x)*3+c])
					best := 256
					for dy := -1; dy <= 1 && best > 0; dy++ {
						for dx := -1; dx <= 1; dx++ {
							xx, yy := x+dx, y+dy
							if xx < 0 || yy < 0 || xx >= q.w || yy >= q.h {
								continue
							}
							d := v - int(q.pix[(yy*q.w+xx)*3+c])
							if d < 0 {
								d = -d
							}
							best = min(best, d)
						}
					}
					sum += best
					n++
					if best > 48 {
						far++
					}
				}
			}
		}
	}
	return float64(sum) / float64(n), float64(far) / float64(n)
}

// pdfImage describes an image XObject of a page.
type pdfImage struct {
	cs    string
	smask bool
}

// pageImages lists the image XObjects of every page of pdf.
func pageImages(t testing.TB, pdf []byte) [][]pdfImage {
	t.Helper()
	ctx, err := api.ReadContext(context.Background(), bytes.NewReader(pdf), model.NewStatelessConfiguration())
	if err != nil {
		t.Fatal(err)
	}
	out := make([][]pdfImage, ctx.PageCount)
	for i := range out {
		d, _, _, err := ctx.PageDict(context.Background(), i+1, false)
		if err != nil {
			t.Fatal(err)
		}
		res, _ := ctx.DereferenceDict(d["Resources"])
		xo, _ := ctx.DereferenceDict(res["XObject"])
		for _, o := range xo {
			sd, _, err := ctx.DereferenceStreamDict(o)
			if err != nil || sd == nil || sd.Dict.NameEntry("Subtype") == nil || *sd.Dict.NameEntry("Subtype") != "Image" {
				continue
			}
			img := pdfImage{smask: sd.Dict["SMask"] != nil}
			if cs := sd.Dict.NameEntry("ColorSpace"); cs != nil {
				img.cs = string(*cs)
			}
			out[i] = append(out[i], img)
		}
	}
	return out
}

// rgb is an 8-bit RGB page.
type rgb struct {
	w, h int
	pix  []byte
}

// rasteriseRGB renders every page of pdf in colour at dpi.
func rasteriseRGB(t testing.TB, pdf string, n, dpi int) []rgb {
	t.Helper()
	dir := t.TempDir()
	cmd := exec.Command("pdftoppm", "-r", strconv.Itoa(dpi), pdf, filepath.Join(dir, "p"))
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("pdftoppm %s: %v %s", pdf, err, b)
	}
	files, _ := filepath.Glob(filepath.Join(dir, "p-*.ppm"))
	if len(files) != n {
		t.Fatalf("%s: %d pages rendered, want %d", pdf, len(files), n)
	}
	out := make([]rgb, n)
	for i, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var w, h, max int
		if _, err := fmt.Sscan(string(b[:min(len(b), 64)]), new(string), &w, &h, &max); err != nil || max != 255 || w*h*3 > len(b) {
			t.Fatalf("%s: not an 8-bit PPM: %v", f, err)
		}
		out[i] = rgb{w, h, b[len(b)-w*h*3:]}
	}
	return out
}
