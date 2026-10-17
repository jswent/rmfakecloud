package exporter

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ddvk/rmfakecloud/internal/storage/exporter/inkpdf"
	"github.com/ddvk/rmfakecloud/internal/storage/exporter/rmlines"
	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

// The test binary doubles as the render worker in builds with the renderer.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == rmlines.WorkerArg {
		os.Exit(rmlines.WorkerMain(os.Stdin, os.Stdout, os.Stderr))
	}
	os.Exit(m.Run())
}

const fixtures = "../../../testdata/rm"

func fixture(t testing.TB, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(fixtures, name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// pdfPage is what the tests check of an output page.
type pdfPage struct {
	w, h    float64
	content string // decoded content streams
}

// readPDF validates pdf with pdfcpu and returns its pages.
func readPDF(t testing.TB, pdf []byte) []pdfPage {
	t.Helper()
	conf := model.NewStatelessConfiguration()
	ctx, err := api.ReadContext(context.Background(), bytes.NewReader(pdf), conf)
	if err != nil {
		t.Fatalf("ReadContext: %v", err)
	}
	if err := api.ValidateContext(context.Background(), ctx); err != nil {
		t.Fatalf("ValidateContext: %v", err)
	}
	pages := make([]pdfPage, ctx.PageCount)
	for i := range pages {
		d, _, attrs, err := ctx.PageDict(context.Background(), i+1, false)
		if err != nil {
			t.Fatalf("page %d: %v", i+1, err)
		}
		pages[i].w, pages[i].h = attrs.MediaBox.Width(), attrs.MediaBox.Height()
		var refs []types.Object
		switch c := d["Contents"].(type) {
		case types.Array:
			refs = c
		case nil:
		default:
			refs = []types.Object{c}
		}
		var sb strings.Builder
		for _, r := range refs {
			sd, _, err := ctx.DereferenceStreamDict(r)
			if err != nil || sd == nil {
				t.Fatalf("page %d contents: %v", i+1, err)
			}
			if err := sd.Decode(); err != nil {
				t.Fatalf("page %d decode: %v", i+1, err)
			}
			sb.Write(sd.Content)
			sb.WriteByte('\n')
		}
		// StampContent wraps strokes in a form XObject: include forms.
		if res, err := ctx.DereferenceDict(d["Resources"]); err == nil && res != nil {
			if xo, err := ctx.DereferenceDict(res["XObject"]); err == nil {
				for _, o := range xo {
					sd, _, err := ctx.DereferenceStreamDict(o)
					if err != nil || sd == nil || sd.Dict.NameEntry("Subtype") == nil || *sd.Dict.NameEntry("Subtype") != "Form" {
						continue
					}
					if err := sd.Decode(); err == nil {
						sb.Write(sd.Content)
						sb.WriteByte('\n')
					}
				}
			}
		}
		pages[i].content = sb.String()
	}
	return pages
}

func checkSize(t testing.TB, n int, p pdfPage, w, h float64) {
	t.Helper()
	if math.Abs(p.w-w) > 0.01 || math.Abs(p.h-h) > 0.01 {
		t.Errorf("page %d is %vx%v pt, want %vx%v", n, p.w, p.h, w, h)
	}
}

func export(t testing.TB, cfg Config, src *Source) ([]byte, *Stats) {
	t.Helper()
	e := New(cfg)
	defer e.Close()
	var out bytes.Buffer
	st, err := e.Export(context.Background(), src, &out)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	return out.Bytes(), st
}

// strokes reports whether a content stream draws lines.
func strokes(content string) bool {
	return strings.Contains(content, " l\n") || strings.Contains(content, " l ")
}

// A notebook with v5 and v3 pages, a page without ink and a v6 page, with
// the v6 renderer disabled (the stub build behaves the same).
func TestExportNotebookWithoutRenderer(t *testing.T) {
	src := &Source{
		Content: []byte(`{"fileType":"notebook","formatVersion":1,"orientation":"portrait","pageCount":4,"pages":["p1","p2","p3","p4"]}`),
		Pages: map[string]Blob{
			"p1":    {Data: fixture(t, "v5/test_v5.rm")},
			"p3":    {Data: fixture(t, "v5/test_v3.rm")},
			"p4":    {Data: fixture(t, "v6/Page_down_test.rm")},
			"stray": {Data: fixture(t, "v5/test_v5.rm")}, // not a page of the document: ignored
		},
	}
	pdf, st := export(t, Config{Disable: true}, src)
	pages := readPDF(t, pdf)
	if len(pages) != 4 {
		t.Fatalf("%d pages, want 4", len(pages))
	}
	for i, p := range pages {
		checkSize(t, i+1, p, 445.3, 593.8)
	}
	if !strokes(pages[0].content) || !strokes(pages[2].content) {
		t.Error("v5/v3 pages have no strokes")
	}
	if strokes(pages[1].content) || strokes(pages[3].content) || strings.Contains(pages[3].content, " Do") {
		t.Error("blank pages have content")
	}
	want := Stats{Pages: 4, V5: 2, Blank: 1, V6Unavailable: 1}
	if *st != want {
		t.Errorf("stats %+v, want %+v", *st, want)
	}
}

// The same document landscape: v5 pages are turned, page sizes swapped.
func TestExportLandscapeNotebook(t *testing.T) {
	src := &Source{
		Content: []byte(`{"fileType":"notebook","formatVersion":1,"orientation":"landscape","pages":["p1","p2"]}`),
		Pages:   map[string]Blob{"p1": {Data: fixture(t, "v5/test_v5.rm")}},
	}
	pdf, _ := export(t, Config{Disable: true}, src)
	pages := readPDF(t, pdf)
	if len(pages) != 2 {
		t.Fatalf("%d pages", len(pages))
	}
	for i, p := range pages {
		checkSize(t, i+1, p, 593.8, 445.3)
	}
}

// backgroundPDF returns a PDF whose page i (1-based) is 100*i points square.
func backgroundPDF(t testing.TB, n int) []byte {
	t.Helper()
	d, err := inkpdf.NewDocument()
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= n; i++ {
		if _, err := d.AddBlankPage(float64(100*i), float64(100*i)); err != nil {
			t.Fatal(err)
		}
	}
	var b bytes.Buffer
	if err := d.Write(&b); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func cPage(id, idx string, redir int) string {
	s := fmt.Sprintf(`{"id":%q,"idx":{"timestamp":"1:2","value":%q}`, id, idx)
	if redir >= 0 {
		s += fmt.Sprintf(`,"redir":{"timestamp":"1:2","value":%d}`, redir)
	}
	return s + "}"
}

// An annotated PDF whose pages were reordered, inserted and deleted on the
// tablet: the output follows the .content order, drops the deleted PDF page,
// and adds inserted pages (and pages past the end of the PDF) at the device
// page size.
func TestExportPDFPageOrder(t *testing.T) {
	pages := []string{
		cPage("a", "ba", 1),  // PDF page 2, with v5 ink
		cPage("b", "bb", -1), // inserted, with v5 ink
		cPage("c", "bc", 0),  // PDF page 1, no ink
		cPage("d", "bd", 7),  // past the end of the 3-page PDF
		// PDF page 3 is not listed: deleted on the tablet
	}
	src := &Source{
		Content: []byte(`{"fileType":"pdf","formatVersion":2,"orientation":"portrait","pageCount":4,` +
			`"cPages":{"original":{"timestamp":"1:1","value":3},"pages":[` + strings.Join(pages, ",") + `]}}`),
		Payload: bytes.NewReader(backgroundPDF(t, 3)),
		Pages: map[string]Blob{
			"a": {Data: fixture(t, "v5/test_v5.rm")},
			"b": {Data: fixture(t, "v5/test_v5.rm")},
		},
	}
	pdf, st := export(t, Config{Disable: true}, src)
	out := readPDF(t, pdf)
	if len(out) != 4 {
		t.Fatalf("%d pages, want 4", len(out))
	}
	checkSize(t, 1, out[0], 200, 200)
	checkSize(t, 2, out[1], 445.3, 593.8)
	checkSize(t, 3, out[2], 100, 100)
	checkSize(t, 4, out[3], 445.3, 593.8)
	if !strokes(out[0].content) || !strokes(out[1].content) {
		t.Error("ink missing on pages 1-2")
	}
	if strokes(out[2].content) || strokes(out[3].content) {
		t.Error("ink on pages without ink")
	}
	if st.V5 != 2 || st.Blank != 2 || st.Failed != 0 {
		t.Errorf("stats %+v", *st)
	}
}

// A PDF that was never opened on the tablet has no page list: the export
// is the PDF itself.
func TestExportPayloadOnly(t *testing.T) {
	src := &Source{
		Content: []byte(`{"fileType":"pdf","pageCount":0}`),
		Payload: bytes.NewReader(backgroundPDF(t, 2)),
	}
	pdf, _ := export(t, Config{Disable: true}, src)
	out := readPDF(t, pdf)
	if len(out) != 2 {
		t.Fatalf("%d pages, want 2", len(out))
	}
	checkSize(t, 2, out[1], 200, 200)
}

// A page that fails to render becomes a blank page; the export succeeds.
func TestExportPageFailure(t *testing.T) {
	broken := append([]byte("reMarkable .lines file, version=5          "), 0xff, 0xff, 0xff, 0xff, 0xff)
	src := &Source{
		Content: []byte(`{"fileType":"notebook","pages":["p1","p2"]}`),
		Pages: map[string]Blob{
			"p1": {Data: broken},
			"p2": {Data: fixture(t, "v5/test_v5.rm")},
		},
	}
	pdf, st := export(t, Config{Disable: true}, src)
	out := readPDF(t, pdf)
	if len(out) != 2 {
		t.Fatalf("%d pages, want 2", len(out))
	}
	if strokes(out[0].content) || !strokes(out[1].content) {
		t.Error("wrong page content after a failure")
	}
	if st.Failed != 1 || st.V5 != 1 {
		t.Errorf("stats %+v", *st)
	}
}

func TestExportBadContent(t *testing.T) {
	e := New(Config{Disable: true})
	if _, err := e.Export(context.Background(), &Source{Content: []byte("nope")}, &bytes.Buffer{}); err == nil {
		t.Fatal("expected an error for an unreadable .content")
	}
	src := &Source{Content: []byte(`{"fileType":"pdf","pages":["a"]}`), Payload: strings.NewReader("not a pdf")}
	if _, err := e.Export(context.Background(), src, &bytes.Buffer{}); err == nil {
		t.Fatal("expected an error for a broken PDF payload")
	}
}

// The sync 1.0 zip: payload, .content, and pages named by index (old
// documents) or by id.
func TestReadZip(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	add := func(name string, data []byte) {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write(data)
	}
	add("doc.content", []byte(`{"fileType":"pdf","formatVersion":1,"pages":["x","y"],"redirectionPageMap":[0,1]}`))
	add("doc.pdf", backgroundPDF(t, 2))
	add("doc.pagedata", []byte("Blank\nBlank\n"))
	add("doc/0.rm", fixture(t, "v5/test_v5.rm"))
	add("doc/y.rm", fixture(t, "v5/test_v3.rm"))
	add("doc/y-metadata.json", []byte(`{}`))
	add("doc.thumbnails/0.jpg", []byte{0xff})
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	src, err := ReadZip(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	if src.Payload == nil || src.Pagedata == nil || len(src.Pages) != 2 {
		t.Fatalf("source %+v", src)
	}
	pdf, st := export(t, Config{Disable: true}, src)
	out := readPDF(t, pdf)
	if len(out) != 2 || !strokes(out[0].content) || !strokes(out[1].content) || st.V5 != 2 {
		t.Fatalf("%d pages, stats %+v", len(out), *st)
	}
	checkSize(t, 1, out[0], 100, 100)
	checkSize(t, 2, out[1], 200, 200)

	if _, err := ReadZip(bytes.NewReader([]byte("PK")), 2); err == nil {
		t.Error("broken zip accepted")
	}
}

// templated reports whether a page carries a template from package
// templates (its rule colour).
func templated(content string) bool { return strings.Contains(content, "0.7529 G") }

func cPageT(id, idx string, redir int, template string) string {
	s := cPage(id, idx, redir)
	return s[:len(s)-1] + fmt.Sprintf(`,"template":{"timestamp":"1:2","value":%q}}`, template)
}

// Notebook pages get their template under the ink; pages of an annotated
// PDF do not, pages inserted into it do; RMLINES_TEMPLATES=false turns it
// off; unknown templates leave the page blank.
func TestExportTemplates(t *testing.T) {
	notebook := &Source{
		Content: []byte(`{"fileType":"notebook","formatVersion":2,"orientation":"portrait","pageCount":4,"cPages":{"pages":[` +
			cPageT("a", "ba", -1, "P Lined heading") + "," + cPageT("b", "bb", -1, "P Grid medium") + "," +
			cPageT("c", "bc", -1, "P Weird unknown") + "," + cPageT("d", "bd", -1, "P Lines small") + `]}}`),
		Pages: map[string]Blob{
			"a": {Data: fixture(t, "v5/test_v5.rm")},
			"d": {Data: fixture(t, "v6/Page_down_test.rm")},
		},
	}
	pdf, _ := export(t, Config{Disable: true}, notebook)
	out := readPDF(t, pdf)
	if len(out) != 4 {
		t.Fatalf("%d pages", len(out))
	}
	for i, want := range []bool{true, true, false, true} {
		if got := templated(out[i].content); got != want {
			t.Errorf("page %d: template drawn %v, want %v", i+1, got, want)
		}
	}
	if !strokes(out[0].content) {
		t.Error("page 1: ink missing")
	}

	pdf, _ = export(t, Config{Disable: true, NoTemplates: true}, notebook)
	for i, p := range readPDF(t, pdf) {
		if templated(p.content) {
			t.Errorf("NoTemplates: page %d has a template", i+1)
		}
	}

	annotated := &Source{
		Content: []byte(`{"fileType":"pdf","formatVersion":2,"orientation":"portrait","pageCount":2,"cPages":{"pages":[` +
			cPageT("a", "ba", 0, "P Lined heading") + "," + cPageT("b", "bb", -1, "P Lined heading") + `]}}`),
		Payload: bytes.NewReader(backgroundPDF(t, 1)),
		Pages:   map[string]Blob{"a": {Data: fixture(t, "v5/test_v5.rm")}},
	}
	pdf, _ = export(t, Config{Disable: true}, annotated)
	out = readPDF(t, pdf)
	if len(out) != 2 || templated(out[0].content) || !templated(out[1].content) {
		t.Errorf("annotated PDF: %d pages, templates %v %v, want PDF page without, inserted page with",
			len(out), len(out) > 0 && templated(out[0].content), len(out) > 1 && templated(out[1].content))
	}
}
