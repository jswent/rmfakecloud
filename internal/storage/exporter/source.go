package exporter

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
)

// Blob is one page's .rm file: either a file already on disk (the sync 1.5
// blob store keeps every blob as a file named after its hash) or its bytes
// (the sync 1.0 zip). librm_lines reads from a path, so the exporter writes
// Data to a temporary file when a v6 page has no Path.
type Blob struct {
	Path string
	Data []byte
}

// header returns the first n bytes of the blob (fewer if it is shorter).
func (b Blob) header(n int) ([]byte, error) {
	if b.Path == "" {
		if len(b.Data) < n {
			return b.Data, nil
		}
		return b.Data[:n], nil
	}
	f, err := os.Open(b.Path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	buf := make([]byte, n)
	m, err := io.ReadFull(f, buf)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return nil, err
	}
	return buf[:m], nil
}

// bytes returns the blob's contents.
func (b Blob) bytes() ([]byte, error) {
	if b.Path == "" {
		return b.Data, nil
	}
	return os.ReadFile(b.Path)
}

// Source is a document to export, independent of how it is stored.
type Source struct {
	// Content is the .content file.
	Content []byte
	// Pagedata is the optional .pagedata file (formatVersion 1 templates).
	Pagedata []byte
	// Payload is the PDF or EPUB the document was made from, nil for
	// notebooks. Its type comes from the .content fileType.
	Payload io.ReadSeeker
	// Pages maps a page id (the .rm file's base name) to its .rm blob. Very
	// old documents name their pages by 0-based index instead; such keys
	// are matched to the page at that position.
	Pages map[string]Blob
}

// ReadZip reads a sync 1.0 document archive (<id>.content, <id>.pdf or
// <id>.epub, <id>.pagedata, <id>/<page>.rm). Everything it needs is read
// into memory; other entries (metadata, thumbnails) are skipped.
func ReadZip(r io.ReaderAt, size int64) (*Source, error) {
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return nil, fmt.Errorf("exporter: open zip: %w", err)
	}
	src := &Source{Pages: map[string]Blob{}}
	var payload []byte
	read := func(f *zip.File) ([]byte, error) {
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		return io.ReadAll(rc)
	}
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		name := f.Name
		ext := path.Ext(name)
		base := strings.TrimSuffix(path.Base(name), ext)
		var data []byte
		switch ext {
		case ".content", ".pagedata", ".pdf", ".epub", ".rm":
			if data, err = read(f); err != nil {
				return nil, fmt.Errorf("exporter: read %s: %w", name, err)
			}
		default:
			continue
		}
		switch ext {
		case ".content":
			src.Content = data
		case ".pagedata":
			src.Pagedata = data
		case ".pdf", ".epub":
			payload = data
		case ".rm":
			src.Pages[base] = Blob{Data: data}
		}
	}
	if src.Content == nil {
		return nil, fmt.Errorf("exporter: zip has no .content file")
	}
	if payload != nil {
		src.Payload = bytes.NewReader(payload)
	}
	return src, nil
}
