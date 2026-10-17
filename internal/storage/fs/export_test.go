package fs

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/ddvk/rmfakecloud/internal/config"
	"github.com/ddvk/rmfakecloud/internal/storage/exporter/inkpdf"
)

func testPDF(t *testing.T, pages int) []byte {
	t.Helper()
	d, err := inkpdf.NewDocument()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < pages; i++ {
		if _, err := d.AddBlankPage(300, 400); err != nil {
			t.Fatal(err)
		}
	}
	var b bytes.Buffer
	if err := d.Write(&b); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func readAll(t *testing.T) func(io.ReadCloser, error) []byte {
	return func(r io.ReadCloser, err error) []byte { return readPDFStream(t, r, err) }
}

func readPDFStream(t *testing.T, r io.ReadCloser, err error) []byte {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(b, []byte("%PDF-")) {
		t.Fatalf("not a PDF: %.20q", b)
	}
	return b
}

func newTestStorage(t *testing.T, uid string) *FileSystemStorage {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, userDir, uid, SyncFolder), 0700); err != nil {
		t.Fatal(err)
	}
	return NewStorage(&config.Config{DataDir: dir, HashSchemaVersion: "3", Render: config.RenderConfig{Disable: true}})
}

func TestExportBlobDocument(t *testing.T) {
	const uid = "test"
	fs := newTestStorage(t, uid)
	doc, err := fs.CreateBlobDocument(uid, "paper.pdf", "", bytes.NewReader(testPDF(t, 2)))
	if err != nil {
		t.Fatal(err)
	}
	first := readAll(t)(fs.Export(uid, doc.ID))
	cached, _ := filepath.Glob(filepath.Join(fs.getPathFromUser(uid, CacheDir), doc.ID+"-*.pdf"))
	if len(cached) != 1 {
		t.Fatalf("cache files %v", cached)
	}
	if again := readAll(t)(fs.Export(uid, doc.ID)); !bytes.Equal(first, again) {
		t.Fatal("second export differs from the cached one")
	}
	if _, err := fs.Export(uid, "no-such-doc"); err == nil {
		t.Fatal("export of a missing document succeeded")
	}
}

func TestExportZipDocument(t *testing.T) {
	const uid = "test"
	fs := newTestStorage(t, uid)
	doc, err := fs.CreateDocument(uid, "paper.pdf", "", io.NopCloser(bytes.NewReader(testPDF(t, 3))))
	if err != nil {
		t.Fatal(err)
	}
	readAll(t)(fs.ExportDocument(uid, doc.ID, "pdf", 0))
	cached, _ := filepath.Glob(filepath.Join(fs.getPathFromUser(uid, CacheDir), doc.ID+"-annotated-*.pdf"))
	if len(cached) != 1 {
		t.Fatalf("cache files %v", cached)
	}
	if _, err := fs.ExportDocument(uid, doc.ID, "epub", 0); err == nil {
		t.Fatal("epub export accepted")
	}
}
