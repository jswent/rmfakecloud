package fs

import (
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/ddvk/rmfakecloud/internal/common"
	"github.com/ddvk/rmfakecloud/internal/storage"
	"github.com/ddvk/rmfakecloud/internal/storage/exporter"
	"github.com/ddvk/rmfakecloud/internal/storage/models"
	log "github.com/sirupsen/logrus"
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
		})
	})
	return fs.exporter
}

// exportCached returns the cached export at cachePath, or renders src there.
// The PDF is written to a temporary file in the same directory and renamed
// into place only when the export succeeded, so a failed export returns an
// error before anything is streamed and never leaves a partial file.
// Older exports of the same document matching stalePattern are removed.
func (fs *FileSystemStorage) exportCached(cachePath, stalePattern string, src func() (*exporter.Source, func(), error)) (io.ReadCloser, error) {
	if f, err := os.Open(cachePath); err == nil {
		return f, nil
	}
	dir := filepath.Dir(cachePath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	s, done, err := src()
	if err != nil {
		return nil, err
	}
	defer done()

	tmp, err := os.CreateTemp(dir, ".export-*.pdf")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmp.Name()) // no-op after the rename
	if _, err := fs.Exporter().Export(context.Background(), s, tmp); err != nil {
		tmp.Close()
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		return nil, err
	}
	if stale, _ := filepath.Glob(stalePattern); len(stale) > 0 {
		for _, f := range stale {
			if f != cachePath {
				os.Remove(f)
			}
		}
	}
	if err := os.Rename(tmp.Name(), cachePath); err != nil {
		return nil, err
	}
	return os.Open(cachePath)
}

// Export renders a sync 1.5 document to PDF. The result is cached per
// document hash and exporter settings.
func (fs *FileSystemStorage) Export(uid, docid string) (io.ReadCloser, error) {
	tree, err := fs.GetCachedTree(uid)
	if err != nil {
		return nil, err
	}
	doc, err := tree.FindDoc(docid)
	if err != nil {
		return nil, err
	}
	id := common.Sanitize(docid)
	cacheDir := fs.getPathFromUser(uid, CacheDir)
	cachePath := path.Join(cacheDir, fmt.Sprintf("%s-%s-%s.pdf", id, common.Sanitize(doc.Hash), fs.Exporter().CacheKey()))
	return fs.exportCached(cachePath, path.Join(cacheDir, id+"-*.pdf"), func() (*exporter.Source, func(), error) {
		return fs.hashDocSource(uid, doc)
	})
}

// hashDocSource reads a sync 1.5 document from the blob store. Page blobs are
// files named after their hash, so the renderer reads them in place.
func (fs *FileSystemStorage) hashDocSource(uid string, doc *models.HashDoc) (*exporter.Source, func(), error) {
	blobDir := fs.getUserBlobPath(uid)
	blobPath := func(hash string) string { return path.Join(blobDir, common.Sanitize(hash)) }
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
		var err error
		switch ext {
		case storage.ContentFileExt:
			src.Content, err = os.ReadFile(blobPath(f.Hash))
		case ".pagedata":
			src.Pagedata, err = os.ReadFile(blobPath(f.Hash))
		case storage.PdfFileExt, storage.EpubFileExt:
			if payload != nil {
				payload.Close()
			}
			payload, err = os.Open(blobPath(f.Hash))
			if err == nil {
				src.Payload = payload
			}
		case storage.RmFileExt:
			p := blobPath(f.Hash)
			if _, serr := os.Stat(p); serr != nil {
				log.Warnf("export %s: page %s: blob %s missing; exporting it blank", doc.EntryName, name, f.Hash)
				continue
			}
			src.Pages[name] = exporter.Blob{Path: p}
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
// cached and rebuilt when the zip is newer.
func (fs *FileSystemStorage) ExportDocument(uid, id, outputType string, exportOption storage.ExportOption) (io.ReadCloser, error) {
	if outputType != "pdf" {
		return nil, fmt.Errorf("export: only pdf is supported, not %q", outputType)
	}
	sanitizedID := common.Sanitize(id)
	zipFilePath := fs.getPathFromUser(uid, sanitizedID+storage.ZipFileExt)
	rawStat, err := os.Stat(zipFilePath)
	if err != nil {
		return nil, fmt.Errorf("cant find raw document %v", err)
	}
	cacheDir := fs.getPathFromUser(uid, CacheDir)
	cachePath := path.Join(cacheDir, sanitizedID+"-annotated-"+fs.Exporter().CacheKey()+".pdf")
	if st, err := os.Stat(cachePath); err == nil && rawStat.ModTime().After(st.ModTime()) {
		os.Remove(cachePath)
	}
	return fs.exportCached(cachePath, path.Join(cacheDir, sanitizedID+"-annotated*.pdf"), func() (*exporter.Source, func(), error) {
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
