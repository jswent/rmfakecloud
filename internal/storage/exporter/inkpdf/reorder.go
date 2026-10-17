package inkpdf

import (
	"context"
	"fmt"

	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu"
)

// Reorder replaces the document's page sequence with pageNrs (1-based page
// numbers of the current document, in output order). Pages may be left out
// or repeated; a repeated page is copied with whatever was stamped on it.
//
// Pages can only be appended, so an export that puts inserted pages between
// background pages, or drops background pages deleted on the tablet,
// assembles everything first and reorders once at the end. Reorder copies
// the selected pages into a new document (pdfcpu.ExtractPages, as used by
// "pdfcpu collect"); document-level structures that refer to pages, such as
// outlines, do not survive it. It is a no-op when pageNrs is 1..PageCount.
// Call it after all stamping, right before Write.
func (d *Document) Reorder(pageNrs []int) error {
	if len(pageNrs) == 0 {
		return fmt.Errorf("inkpdf: reorder: no pages")
	}
	identity := len(pageNrs) == d.ctx.PageCount
	for i, n := range pageNrs {
		if n < 1 || n > d.ctx.PageCount {
			return fmt.Errorf("inkpdf: reorder: page %d out of range (1..%d)", n, d.ctx.PageCount)
		}
		if n != i+1 {
			identity = false
		}
	}
	if identity {
		return nil
	}
	ctx, err := pdfcpu.ExtractPages(context.Background(), d.ctx, pageNrs, false)
	if err != nil {
		return fmt.Errorf("inkpdf: reorder: %w", err)
	}
	if err := ctx.EnsurePageCount(); err != nil {
		return fmt.Errorf("inkpdf: reorder: %w", err)
	}
	d.ctx = ctx
	// The shared objects belong to the old xref table.
	d.qRef, d.multiplyRef = nil, nil
	return nil
}
