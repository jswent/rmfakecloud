package content

import (
	"errors"

	log "github.com/sirupsen/logrus"
)

// PlanPage is one output page.
type PlanPage struct {
	ID string
	// Format is the detected format of the page's .rm blob, FormatUnknown
	// when there is no blob (a blank page) or its header is not recognised.
	Format Format
	// HasInk is true when a .rm blob exists for the page. The blob may still
	// hold no strokes.
	HasInk bool
	// Background is the 1-based payload page under this page, 0 for none
	// (same as Page.Redir, after the fallback described on BuildPlan).
	Background int
	Template   string
}

// Plan is the ordered list of pages to export.
type Plan struct {
	Pages       []PlanPage
	Landscape   bool
	PayloadType string // "", "pdf", "epub"
}

// BuildPlan turns parsed content and the formats of the available .rm blobs
// (page id -> format) into a page plan.
//
// Rules:
//   - one PlanPage per page of c.Pages (deleted pages are already gone), in
//     order;
//   - a page without a blob is kept as a blank page (HasInk false);
//   - a blob whose id is not a page of the document is ignored and logged at
//     debug level;
//   - assumption: for a "pdf" payload whose .content carries no redirection
//     at all (no redirectionPageMap and no cPages redir), page i is taken to
//     annotate payload page i+1, provided the page count matches the payload
//     (originalPageCount when known, else pageCount; neither known counts as
//     a match). This is how formatVersion 1 documents without a
//     redirectionPageMap behaved, and how rmapi pairs pages. The orchestrator
//     still has to drop backgrounds past the end of the actual PDF.
func BuildPlan(c *Content, rmFormats map[string]Format) (*Plan, error) {
	if c == nil {
		return nil, errors.New("content: BuildPlan: nil content")
	}
	plan := &Plan{
		Pages:     make([]PlanPage, 0, len(c.Pages)),
		Landscape: c.Orientation == "landscape",
	}
	switch c.FileType {
	case "pdf", "epub":
		plan.PayloadType = c.FileType
	}

	identity := plan.PayloadType == "pdf" && !c.HasRedirection && identityApplies(c)
	if identity {
		log.Debug("content: no redirection in .content; pairing page i with PDF page i")
	}

	known := make(map[string]bool, len(c.Pages))
	for i, p := range c.Pages {
		known[p.ID] = true
		f, ok := rmFormats[p.ID]
		pp := PlanPage{
			ID:         p.ID,
			Format:     f,
			HasInk:     ok,
			Background: p.Redir,
			Template:   p.Template,
		}
		if !ok {
			pp.Format = FormatUnknown
		}
		if identity {
			pp.Background = i + 1
		}
		plan.Pages = append(plan.Pages, pp)
	}
	for id := range rmFormats {
		if !known[id] {
			log.Debugf("content: ignoring .rm blob %s: not a page of the document", id)
		}
	}
	return plan, nil
}

func identityApplies(c *Content) bool {
	n := len(c.Pages)
	if n == 0 {
		return false
	}
	for _, p := range c.Pages {
		if p.Redir != 0 {
			return false
		}
	}
	payload := c.OriginalPageCount
	if payload <= 0 {
		payload = c.PageCount
	}
	return payload <= 0 || payload == n
}
