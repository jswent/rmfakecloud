package inkpdf

import (
	"bytes"
	"context"
	"image"
	"math"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

func openBytes(t testing.TB, pdf []byte) *Document {
	t.Helper()
	d, err := Open(bytes.NewReader(pdf), OpenOptions{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return d
}

func writeBytes(t testing.TB, d *Document) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := d.Write(&b); err != nil {
		t.Fatalf("Write: %v", err)
	}
	return b.Bytes()
}

// readValidated parses pdf with pdfcpu (independently of Document) and runs
// api.ValidateContext on it.
func readValidated(t testing.TB, pdf []byte, conf *model.Configuration) *model.Context {
	t.Helper()
	if conf == nil {
		conf = model.NewStatelessConfiguration()
	}
	ctx, err := api.ReadContext(context.Background(), bytes.NewReader(pdf), conf)
	if err != nil {
		t.Fatalf("ReadContext: %v", err)
	}
	if err := api.ValidateContext(context.Background(), ctx); err != nil {
		t.Fatalf("ValidateContext: %v", err)
	}
	return ctx
}

func pageDict(t testing.TB, ctx *model.Context, n int) (types.Dict, *model.InheritedPageAttrs) {
	t.Helper()
	d, _, attrs, err := ctx.PageDict(context.Background(), n, false)
	if err != nil || d == nil {
		t.Fatalf("PageDict(%d): %v", n, err)
	}
	return d, attrs
}

// entry returns the PDF serialisation of d[key] ("" when absent).
func entry(d types.Dict, key string) string {
	o, ok := d[key]
	if !ok || o == nil {
		return ""
	}
	return o.PDFString()
}

func decodeStream(t testing.TB, ctx *model.Context, o types.Object) string {
	t.Helper()
	sd, _, err := ctx.DereferenceStreamDict(o)
	if err != nil || sd == nil {
		t.Fatalf("stream %v: %v", o, err)
	}
	if err := sd.Decode(); err != nil {
		t.Fatalf("decode %v: %v", o, err)
	}
	return string(sd.Content)
}

// contents returns the decoded streams of a page's /Contents, in order.
func contents(t testing.TB, ctx *model.Context, page types.Dict) []string {
	t.Helper()
	o, ok := page["Contents"]
	if !ok {
		return nil
	}
	obj, err := ctx.Dereference(o)
	if err != nil {
		t.Fatal(err)
	}
	if a, ok := obj.(types.Array); ok {
		var out []string
		for _, e := range a {
			out = append(out, decodeStream(t, ctx, e))
		}
		return out
	}
	return []string{decodeStream(t, ctx, o)}
}

var cmRe = regexp.MustCompile(`(-?[\d.]+) (-?[\d.]+) (-?[\d.]+) (-?[\d.]+) (-?[\d.]+) (-?[\d.]+) cm`)

// cmOf returns the operands of the only cm operator in s.
func cmOf(t testing.TB, s string) matrix {
	t.Helper()
	all := cmRe.FindAllStringSubmatch(s, -1)
	if len(all) != 1 {
		t.Fatalf("want exactly one cm in %q, got %d", s, len(all))
	}
	var m matrix
	for i := range m {
		v, err := strconv.ParseFloat(all[0][i+1], 64)
		if err != nil {
			t.Fatal(err)
		}
		m[i] = v
	}
	return m
}

func sameMatrix(a, b matrix) bool {
	for i := range a {
		if math.Abs(a[i]-b[i]) > 1e-3 {
			return false
		}
	}
	return true
}

// xobjectName returns the name drawn by the "/Name Do" operator in s.
func xobjectName(t testing.TB, s string) string {
	t.Helper()
	m := regexp.MustCompile(`/(\S+) Do`).FindAllStringSubmatch(s, -1)
	if len(m) != 1 {
		t.Fatalf("want one Do in %q", s)
	}
	return m[0][1]
}

func resourceEntry(t testing.TB, ctx *model.Context, res types.Dict, kind, name string) types.Object {
	t.Helper()
	sub, err := ctx.DereferenceDict(res[kind])
	if err != nil || sub == nil {
		t.Fatalf("resources /%s: %v (%v)", kind, err, res)
	}
	o, ok := sub[name]
	if !ok {
		t.Fatalf("resources /%s has no /%s: %v", kind, name, sub)
	}
	return o
}

func hasResource(ctx *model.Context, res types.Dict, kind, name string) bool {
	sub, err := ctx.DereferenceDict(res[kind])
	if err != nil || sub == nil {
		return false
	}
	_, ok := sub[name]
	return ok
}

// checkStamped asserts the structure of a stamped page against the same page
// in the original: boxes, /Rotate and /Annots byte-for-byte unchanged, the
// original content intact between "q" and the appended stream. It returns the
// appended stream.
func checkStamped(t testing.TB, orig, out *model.Context, n int, stamps int) string {
	t.Helper()
	od, _ := pageDict(t, orig, n)
	nd, _ := pageDict(t, out, n)
	for _, k := range []string{"MediaBox", "CropBox", "Rotate", "Annots", "UserUnit"} {
		if a, b := entry(od, k), entry(nd, k); a != b {
			t.Errorf("page %d /%s changed: %q -> %q", n, k, a, b)
		}
	}
	oc := contents(t, orig, od)
	nc := contents(t, out, nd)
	if len(nc) != len(oc)+2*stamps {
		t.Fatalf("page %d: %d content streams, want %d", n, len(nc), len(oc)+2*stamps)
	}
	for i := 0; i < stamps; i++ {
		if strings.TrimSpace(nc[i]) != "q" {
			t.Errorf("page %d: stream %d = %q, want q", n, i, nc[i])
		}
	}
	for i, s := range oc {
		if nc[stamps+i] != s {
			t.Errorf("page %d: original stream %d changed", n, i)
		}
	}
	last := nc[len(nc)-1]
	if !strings.HasPrefix(strings.TrimSpace(last), "Q") || !strings.HasSuffix(strings.TrimSpace(last), "Q") {
		t.Errorf("page %d: appended stream not Q…Q wrapped: %q", n, last)
	}
	return last
}

func imgRect(img image.Image) image.Rectangle { return img.Bounds().Sub(img.Bounds().Min) }
