//go:build cgo && rmlines

package exporter

import (
	"bytes"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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
