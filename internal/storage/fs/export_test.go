package fs

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

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
	first := readAll(t)(fs.Export(context.Background(), uid, doc.ID))
	cached, _ := filepath.Glob(filepath.Join(fs.getPathFromUser(uid, CacheDir), doc.ID+"-*.pdf"))
	if len(cached) != 1 {
		t.Fatalf("cache files %v", cached)
	}
	if again := readAll(t)(fs.Export(context.Background(), uid, doc.ID)); !bytes.Equal(first, again) {
		t.Fatal("second export differs from the cached one")
	}
	if _, err := fs.Export(context.Background(), uid, "no-such-doc"); err == nil {
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
	first := readAll(t)(fs.ExportDocument(context.Background(), uid, doc.ID, "pdf", 0))
	cached, _ := filepath.Glob(filepath.Join(fs.getPathFromUser(uid, CacheDir), doc.ID+"-annotated-*.pdf"))
	if len(cached) != 1 {
		t.Fatalf("cache files %v", cached)
	}
	if _, err := fs.ExportDocument(context.Background(), uid, doc.ID, "epub", 0); err == nil {
		t.Fatal("epub export accepted")
	}

	// A replaced zip (other size and time) is exported afresh even when its
	// modification time is older than the cached export; the older export
	// goes, a newer unrelated one stays.
	zipPath := fs.getPathFromUser(uid, doc.ID+".zip")
	other, err := fs.CreateDocument(uid, "other.pdf", "", io.NopCloser(bytes.NewReader(testPDF(t, 1))))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(fs.getPathFromUser(uid, other.ID+".zip"), zipPath); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(zipPath, past, past); err != nil {
		t.Fatal(err)
	}
	newer := filepath.Join(fs.getPathFromUser(uid, CacheDir), doc.ID+"-annotated-newer.pdf")
	if err := os.WriteFile(newer, nil, 0600); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(newer, future, future); err != nil {
		t.Fatal(err)
	}
	pdf := readAll(t)(fs.ExportDocument(context.Background(), uid, doc.ID, "pdf", 0))
	if bytes.Equal(pdf, first) {
		t.Error("the replaced zip was served from the old export")
	}
	now, _ := filepath.Glob(filepath.Join(fs.getPathFromUser(uid, CacheDir), doc.ID+"-annotated-*.pdf"))
	if len(now) != 2 {
		t.Fatalf("cache files after the replacement %v (before %v)", now, cached)
	}
	for _, f := range now {
		if f == cached[0] {
			t.Errorf("stale export %s kept", f)
		}
	}
	if _, err := os.Stat(newer); err != nil {
		t.Errorf("newer export removed: %v", err)
	}
}

// Concurrent requests share one render.
func TestExportFlightsShare(t *testing.T) {
	g := &exportFlights{flights: map[string]*exportFlight{}}
	const n = 8
	var calls atomic.Int32
	release := make(chan struct{})
	render := func(ctx context.Context) error {
		calls.Add(1)
		<-release
		return nil
	}
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		go func() { errs <- g.do(context.Background(), "k", time.Minute, render) }()
	}
	waitFor(t, func() bool {
		g.mu.Lock()
		defer g.mu.Unlock()
		return g.flights["k"] != nil && g.flights["k"].waiters == n
	})
	close(release)
	for i := 0; i < n; i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	if c := calls.Load(); c != 1 {
		t.Fatalf("%d renders, want 1", c)
	}
	if len(g.flights) != 0 {
		t.Fatalf("flights left: %v", g.flights)
	}
}

// A waiter that leaves does not cancel the render for the others; when all
// have left it is cancelled. The document deadline applies.
func TestExportFlightsCancel(t *testing.T) {
	g := &exportFlights{flights: map[string]*exportFlight{}}
	started := make(chan struct{})
	release := make(chan struct{})
	var renderErr error
	render := func(ctx context.Context) error {
		close(started)
		select {
		case <-release:
		case <-ctx.Done():
		}
		renderErr = ctx.Err()
		return renderErr
	}
	ctx1, cancel1 := context.WithCancel(context.Background())
	e1 := make(chan error, 1)
	go func() { e1 <- g.do(ctx1, "k", time.Minute, render) }()
	<-started
	e2 := make(chan error, 1)
	go func() { e2 <- g.do(context.Background(), "k", time.Minute, render) }()
	waitFor(t, func() bool {
		g.mu.Lock()
		defer g.mu.Unlock()
		return g.flights["k"].waiters == 2
	})
	cancel1()
	if err := <-e1; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled waiter: %v", err)
	}
	close(release)
	if err := <-e2; err != nil || renderErr != nil {
		t.Fatalf("remaining waiter: %v, render: %v", err, renderErr)
	}

	// The last waiter leaving cancels the render.
	ctx3, cancel3 := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- g.do(ctx3, "k", time.Minute, func(ctx context.Context) error {
			cancel3()
			<-ctx.Done()
			return ctx.Err()
		})
	}()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("abandoned export: %v", err)
	}

	// Deadline.
	err := g.do(context.Background(), "k", 20*time.Millisecond, func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("document timeout: %v", err)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting")
		}
		time.Sleep(time.Millisecond)
	}
}
