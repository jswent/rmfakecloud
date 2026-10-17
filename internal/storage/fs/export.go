package fs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/ddvk/rmfakecloud/internal/common"
	"github.com/ddvk/rmfakecloud/internal/storage"
	"github.com/ddvk/rmfakecloud/internal/storage/exporter"
	"github.com/ddvk/rmfakecloud/internal/storage/models"
	log "github.com/sirupsen/logrus"
	"golang.org/x/sync/singleflight"
)

// Exporter returns the document exporter, creating it on first use. Its
// render workers start on the first page that needs them.
func (fs *FileSystemStorage) Exporter() *exporter.Exporter {
	fs.exporterOnce.Do(func() {
		r := fs.Cfg.Render
		fs.exporter = exporter.New(exporter.Config{
			Disable:     r.Disable,
			Workers:     r.Workers,
			Scale:       r.Scale,
			Supersample: r.Supersample,
			Timeout:     r.Timeout,
			PtPerUnit:   r.PtPerPx,
			NoClip:      r.NoClip,
			NoTemplates: r.NoTemplates,
		})
	})
	return fs.exporter
}

// DefaultDocTimeout bounds the export of one document when
// RMLINES_DOC_TIMEOUT is not set.
const DefaultDocTimeout = 10 * time.Minute

// exports coordinates concurrent exports of the same document, keyed by the
// cache path (an absolute path, so one group serves every storage).
var exports = &exportFlights{flights: map[string]*exportFlight{}}

type exportFlights struct {
	group   singleflight.Group
	mu      sync.Mutex
	flights map[string]*exportFlight
}

// exportFlight is the context of one export shared by its waiters. It is
// detached from the request that started it, so that request going away
// does not fail the others, and cancelled when the last waiter leaves.
type exportFlight struct {
	ctx     context.Context
	cancel  context.CancelFunc
	waiters int
}

// do runs render once for key however many requests ask for it at the same
// time. render gets a context with the per-document deadline that is
// cancelled when every waiter's ctx is done.
func (g *exportFlights) do(ctx context.Context, key string, timeout time.Duration, render func(context.Context) error) error {
	for {
		g.mu.Lock()
		fl := g.flights[key]
		if fl == nil {
			fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
			fl = &exportFlight{ctx: fctx, cancel: cancel}
			g.flights[key] = fl
		}
		fl.waiters++
		g.mu.Unlock()

		ch := g.group.DoChan(key, func() (any, error) {
			err := render(fl.ctx)
			g.mu.Lock()
			if g.flights[key] == fl {
				delete(g.flights, key) // later requests start afresh
			}
			g.mu.Unlock()
			return nil, err
		})
		var err error
		select {
		case r := <-ch:
			err = r.Err
		case <-ctx.Done():
			err = ctx.Err()
		}
		g.mu.Lock()
		if fl.waiters--; fl.waiters == 0 {
			fl.cancel()
			if g.flights[key] == fl {
				delete(g.flights, key)
			}
		}
		g.mu.Unlock()
		// We joined an export whose waiters had all left just before; it
		// was cancelled on their behalf, not ours: run it again.
		if errors.Is(err, context.Canceled) && ctx.Err() == nil {
			continue
		}
		return err
	}
}

// exportCached returns the cached export at cachePath, or renders src there.
// The PDF is written to a temporary file in the same directory and renamed
// into place only when the export succeeded, so a failed export returns an
// error before anything is streamed and never leaves a partial file.
// Concurrent requests for the same cachePath share one export. Older exports
// of the same document matching stalePattern are removed afterwards.
func (fs *FileSystemStorage) exportCached(ctx context.Context, cachePath, stalePattern string, src func() (*exporter.Source, func(), error)) (io.ReadCloser, error) {
	if f, err := os.Open(cachePath); err == nil {
		return f, nil
	}
	timeout := fs.Cfg.Render.DocTimeout
	if timeout <= 0 {
		timeout = DefaultDocTimeout
	}
	err := exports.do(ctx, cachePath, timeout, func(ctx context.Context) error {
		if _, err := os.Stat(cachePath); err == nil {
			return nil // finished by an export that just ended
		}
		return fs.renderTo(ctx, cachePath, stalePattern, src)
	})
	if err != nil {
		return nil, err
	}
	return os.Open(cachePath)
}

func (fs *FileSystemStorage) renderTo(ctx context.Context, cachePath, stalePattern string, src func() (*exporter.Source, func(), error)) error {
	dir := filepath.Dir(cachePath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	s, done, err := src()
	if err != nil {
		return err
	}
	defer done()

	tmp, err := os.CreateTemp(dir, ".export-*.pdf")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op after the rename
	if _, err := fs.Exporter().Export(ctx, s, tmp); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), cachePath); err != nil {
		return err
	}
	removeOlder(cachePath, stalePattern)
	return nil
}

// removeOlder removes the files matching pattern that are older than keep:
// earlier exports of the same document. Newer ones may belong to an export
// that finished meanwhile (another version or other settings) and stay.
func removeOlder(keep, pattern string) {
	ks, err := os.Stat(keep)
	if err != nil {
		return
	}
	stale, _ := filepath.Glob(pattern)
	for _, f := range stale {
		if f == keep {
			continue
		}
		if st, err := os.Stat(f); err == nil && st.ModTime().Before(ks.ModTime()) {
			os.Remove(f)
		}
	}
}

// safeNameRe is what a name joined into an export path may look like:
// document ids are UUIDs, blob hashes hex, the exporter cache key hex.
var safeNameRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// errUnsafeName is returned for ids and hashes that cannot be file names.
var errUnsafeName = errors.New("export: invalid id")

// safeName returns s if it is a plain file name component and an error
// otherwise. common.Sanitize would turn "../../x" into "x"; a name that
// Sanitize changes is rejected rather than silently mapped onto another
// document. The regexp check is also what static analysis (CodeQL
// go/path-injection) recognises as a path sanitizer.
func safeName(s string) (string, error) {
	if common.Sanitize(s) != s || !safeNameRe.MatchString(s) {
		return "", fmt.Errorf("%w: %q", errUnsafeName, s)
	}
	return s, nil
}

// Export renders a sync 1.5 document to PDF. The result is cached per
// document hash and exporter settings.
func (fs *FileSystemStorage) Export(ctx context.Context, uid, docid string) (io.ReadCloser, error) {
	tree, err := fs.GetCachedTree(uid)
	if err != nil {
		return nil, err
	}
	doc, err := tree.FindDoc(docid)
	if err != nil {
		return nil, err
	}
	id, err := safeName(docid)
	if err != nil {
		return nil, err
	}
	hash, err := safeName(doc.Hash)
	if err != nil {
		return nil, err
	}
	key, err := safeName(fs.Exporter().CacheKey())
	if err != nil {
		return nil, err
	}
	cacheDir := fs.getPathFromUser(uid, CacheDir)
	cachePath := path.Join(cacheDir, fmt.Sprintf("%s-%s-%s.pdf", id, hash, key))
	return fs.exportCached(ctx, cachePath, path.Join(cacheDir, id+"-*.pdf"), func() (*exporter.Source, func(), error) {
		return fs.hashDocSource(uid, doc)
	})
}

// hashDocSource reads a sync 1.5 document from the blob store. Page blobs are
// files named after their hash, so the renderer reads them in place.
func (fs *FileSystemStorage) hashDocSource(uid string, doc *models.HashDoc) (*exporter.Source, func(), error) {
	blobDir := fs.getUserBlobPath(uid)
	src := &exporter.Source{Pages: map[string]exporter.Blob{}}
	var payload *os.File
	done := func() {
		if payload != nil {
			payload.Close()
		}
	}
	for _, f := range doc.Files {
		ext := path.Ext(f.EntryName)
		name := strings.TrimSuffix(path.Base(f.EntryName), ext)
		hash, err := safeName(f.Hash)
		if err != nil {
			done()
			return nil, nil, fmt.Errorf("export %s: %s: %w", doc.EntryName, f.EntryName, err)
		}
		blob := path.Join(blobDir, hash)
		switch ext {
		case storage.ContentFileExt:
			src.Content, err = os.ReadFile(blob)
		case ".pagedata":
			src.Pagedata, err = os.ReadFile(blob)
		case storage.PdfFileExt, storage.EpubFileExt:
			if payload != nil {
				payload.Close()
			}
			payload, err = os.Open(blob)
			if err == nil {
				src.Payload = payload
			}
		case storage.RmFileExt:
			if _, serr := os.Stat(blob); serr != nil {
				log.Warnf("export %s: page %s: blob %s missing; exporting it blank", doc.EntryName, name, f.Hash)
				continue
			}
			src.Pages[name] = exporter.Blob{Path: blob}
		}
		if err != nil {
			done()
			return nil, nil, fmt.Errorf("export %s: %s: %w", doc.EntryName, f.EntryName, err)
		}
	}
	if src.Content == nil {
		done()
		return nil, nil, fmt.Errorf("export %s: document has no .content", doc.EntryName)
	}
	return src, done, nil
}

// ExportDocument exports a sync 1.0 document (a zip) to PDF. The result is
// cached per zip size and modification time, so a replaced zip is exported
// afresh.
func (fs *FileSystemStorage) ExportDocument(ctx context.Context, uid, id, outputType string, exportOption storage.ExportOption) (io.ReadCloser, error) {
	if outputType != "pdf" {
		return nil, fmt.Errorf("export: only pdf is supported, not %q", outputType)
	}
	sanitizedID, err := safeName(id)
	if err != nil {
		return nil, err
	}
	key, err := safeName(fs.Exporter().CacheKey())
	if err != nil {
		return nil, err
	}
	zipFilePath := fs.getPathFromUser(uid, sanitizedID+storage.ZipFileExt)
	rawStat, err := os.Stat(zipFilePath)
	if err != nil {
		return nil, fmt.Errorf("cant find raw document %v", err)
	}
	cacheDir := fs.getPathFromUser(uid, CacheDir)
	cachePath := path.Join(cacheDir, fmt.Sprintf("%s-annotated-%d-%d-%s.pdf",
		sanitizedID, rawStat.Size(), rawStat.ModTime().UnixNano(), key))
	return fs.exportCached(ctx, cachePath, path.Join(cacheDir, sanitizedID+"-annotated*.pdf"), func() (*exporter.Source, func(), error) {
		f, err := os.Open(zipFilePath)
		if err != nil {
			return nil, nil, err
		}
		defer f.Close()
		src, err := exporter.ReadZip(f, rawStat.Size())
		if err != nil {
			return nil, nil, err
		}
		return src, func() {}, nil
	})
}
