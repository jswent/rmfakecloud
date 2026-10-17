// Package content reads a reMarkable document's .content file into an
// ordered page list, detects the format of each page's .rm file and builds
// the page plan the exporter renders from.
//
// The .content schema is undocumented by reMarkable. What this package relies
// on, and which parts are assumptions, is written down in testdata/README.md.
package content

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	log "github.com/sirupsen/logrus"
)

// Page is one page of a document, in document order.
type Page struct {
	// ID is the page uuid, which is also the base name of its .rm file.
	ID string
	// Index is the 0-based position in document order after deleted pages
	// are removed.
	Index int
	// Deleted is always false for pages in Content.Pages, which leaves
	// deleted pages out.
	Deleted bool
	// Template is the template name as the device stores it ("Blank",
	// "P Grid medium", "LS Lines medium", ...), or "" if absent.
	Template string
	// Redir is the 1-based page number of the payload PDF/EPUB this page
	// annotates; 0 means an inserted page with no background.
	Redir int
	// Tags are the names of the page tags attached to this page.
	Tags []string
}

// PageTag is a tag attached to a page (formatVersion 2 object form). Tags
// stored in the older string form have only Name set.
type PageTag struct {
	Name      string
	PageID    string
	Timestamp int64
}

// PageSource says which list of the .content file the pages came from.
type PageSource int

const (
	// SourceNone means neither list was present (or both were empty).
	SourceNone PageSource = iota
	// SourcePages is the formatVersion 1 flat "pages" array.
	SourcePages
	// SourceCPages is the formatVersion 2 "cPages.pages" array.
	SourceCPages
)

// Content is the part of a .content file the exporter needs.
type Content struct {
	// FormatVersion is 1 or 2, or 0 if the file does not say.
	FormatVersion int
	// FileType is "", "notebook", "pdf" or "epub" (lower-cased as stored).
	FileType string
	// Orientation is "portrait" or "landscape"; anything else, including
	// absent, reads as "portrait".
	Orientation string
	// PageCount is the stored pageCount. It may disagree with len(Pages).
	PageCount int
	// CoverPage is coverPageNumber: 0 is the first page, -1 the last opened.
	CoverPage int
	// Pages is the document's pages in order, deleted pages excluded, with
	// Index set.
	Pages []Page

	// OriginalPageCount is originalPageCount, or cPages.original.value when
	// that field is absent: the page count of the payload when it was
	// imported. 0 if unknown (the device writes -1 for notebooks).
	OriginalPageCount int
	// LastOpenedPageID is cPages.lastOpened.value, "" if absent.
	LastOpenedPageID string
	// HasRedirection is true when the file states backgrounds explicitly: a
	// redirectionPageMap was present, or at least one cPages entry had a
	// redir. BuildPlan only guesses backgrounds when it is false.
	HasRedirection bool
	// Source is the list the pages were taken from.
	Source PageSource
	// PageTags is every page tag in the file, including ones whose page is
	// deleted or unknown.
	PageTags []PageTag
}

// ErrNoPageList is matched (via errors.Is) by the error Parse returns when the
// file declares pages but lists none.
var ErrNoPageList = errors.New("content: no page list")

// NoPageListError reports a .content file with pageCount > 0 but neither a
// "pages" array nor "cPages.pages". Parse still returns a usable *Content
// (with no pages) alongside it, so a caller can fall back to a best-effort
// export.
type NoPageListError struct {
	PageCount int
}

func (e *NoPageListError) Error() string {
	return fmt.Sprintf("content: pageCount is %d but the file has neither pages nor cPages.pages", e.PageCount)
}

// Is makes errors.Is(err, ErrNoPageList) true.
func (e *NoPageListError) Is(target error) bool { return target == ErrNoPageList }

// Parse decodes a .content file.
//
// Decoding is lenient: unknown fields are ignored and a field of an
// unexpected type is skipped (logged at debug level) rather than failing the
// whole file. Parse fails only when data is not a JSON object. When the file
// has no page list but pageCount > 0, Parse returns both a *Content with no
// pages and a *NoPageListError.
func Parse(data []byte) (*Content, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("content: not a JSON object: %w", err)
	}
	if raw == nil {
		return nil, errors.New("content: not a JSON object: null")
	}

	c := &Content{}
	c.FormatVersion, _ = intField(raw, "formatVersion")
	c.FileType = strings.ToLower(strings.TrimSpace(stringField(raw, "fileType")))
	c.Orientation = "portrait"
	if strings.EqualFold(strings.TrimSpace(stringField(raw, "orientation")), "landscape") {
		c.Orientation = "landscape"
	}
	c.PageCount, _ = intField(raw, "pageCount")
	c.CoverPage, _ = intField(raw, "coverPageNumber")
	c.PageTags = decodePageTags(raw["pageTags"])

	cp := decodeCPages(raw["cPages"])
	if n, ok := intField(raw, "originalPageCount"); ok {
		c.OriginalPageCount = n
	} else if cp.original != nil {
		c.OriginalPageCount = *cp.original
	}
	if c.OriginalPageCount < 0 {
		c.OriginalPageCount = 0
	}
	c.LastOpenedPageID = cp.lastOpened

	flat, hasFlat := decodeFlatPages(raw)
	useCPages := chooseCPages(c.FormatVersion, len(flat) > 0, len(cp.pages) > 0)

	switch {
	case useCPages:
		c.Source = SourceCPages
		c.Pages, c.HasRedirection = cp.ordered()
	case hasFlat && len(flat) > 0:
		c.Source = SourcePages
		c.Pages = flat
		m, ok := raw["redirectionPageMap"]
		c.HasRedirection = ok && !isNull(m)
	}

	c.Pages = dedupe(c.Pages)
	attachTags(c.Pages, c.PageTags)
	for i := range c.Pages {
		c.Pages[i].Index = i
	}

	if len(c.Pages) == 0 && c.PageCount > 0 {
		return c, &NoPageListError{PageCount: c.PageCount}
	}
	return c, nil
}

// chooseCPages picks the page list. A file that names its formatVersion is
// read from that version's list; one that does not (older or hand-made files)
// prefers cPages, which is the list the device keeps up to date. Either way an
// empty preferred list falls back to the other one.
func chooseCPages(formatVersion int, haveFlat, haveC bool) bool {
	if formatVersion == 1 {
		return !haveFlat && haveC
	}
	return haveC
}

// ApplyPagedata fills in templates from a formatVersion 1 .pagedata file,
// which holds one template name per line in page order. Pages that already
// have a template keep it.
func ApplyPagedata(c *Content, pagedata []byte) {
	if c == nil {
		return
	}
	lines := strings.Split(string(pagedata), "\n")
	for i := range c.Pages {
		if i >= len(lines) {
			return
		}
		if c.Pages[i].Template != "" {
			continue
		}
		c.Pages[i].Template = strings.TrimSpace(lines[i])
	}
}

// decodeFlatPages reads the formatVersion 1 "pages" and "redirectionPageMap"
// arrays. redirectionPageMap[i] is the 0-based payload page shown under page
// i; -1, or no entry, is an inserted page.
func decodeFlatPages(raw map[string]json.RawMessage) ([]Page, bool) {
	msg, ok := raw["pages"]
	if !ok || isNull(msg) {
		return nil, false
	}
	var items []json.RawMessage
	if err := json.Unmarshal(msg, &items); err != nil {
		log.Debugf("content: ignoring pages: %v", err)
		return nil, false
	}
	var redir []json.RawMessage
	if m, ok := raw["redirectionPageMap"]; ok && !isNull(m) {
		if err := json.Unmarshal(m, &redir); err != nil {
			log.Debugf("content: ignoring redirectionPageMap: %v", err)
			redir = nil
		}
	}

	pages := make([]Page, 0, len(items))
	for i, item := range items {
		var id string
		if err := json.Unmarshal(item, &id); err != nil || id == "" {
			log.Debugf("content: skipping pages[%d]: not a page id", i)
			continue
		}
		p := Page{ID: id}
		if i < len(redir) {
			if v, ok := toInt(redir[i]); ok && v >= 0 {
				p.Redir = v + 1
			}
		}
		pages = append(pages, p)
	}
	return pages, true
}

type cPage struct {
	pos      int
	id       string
	idx      string
	hasIdx   bool
	template string
	redir    *int
	deleted  bool
}

type cPages struct {
	pages      []cPage
	original   *int
	lastOpened string
}

// lww is the {"timestamp": "1:2", "value": ...} wrapper the device puts
// around every cPages field.
type lww struct {
	Value json.RawMessage `json:"value"`
}

func decodeLWW(msg json.RawMessage) (json.RawMessage, bool) {
	if len(msg) == 0 || isNull(msg) {
		return nil, false
	}
	var v lww
	if err := json.Unmarshal(msg, &v); err != nil || len(v.Value) == 0 || isNull(v.Value) {
		return nil, false
	}
	return v.Value, true
}

func decodeCPages(msg json.RawMessage) cPages {
	var out cPages
	if len(msg) == 0 || isNull(msg) {
		return out
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(msg, &raw); err != nil {
		log.Debugf("content: ignoring cPages: %v", err)
		return out
	}
	if v, ok := decodeLWW(raw["original"]); ok {
		if n, ok := toInt(v); ok {
			out.original = &n
		}
	}
	if v, ok := decodeLWW(raw["lastOpened"]); ok {
		_ = json.Unmarshal(v, &out.lastOpened)
	}

	var items []json.RawMessage
	if m, ok := raw["pages"]; ok && !isNull(m) {
		if err := json.Unmarshal(m, &items); err != nil {
			log.Debugf("content: ignoring cPages.pages: %v", err)
			return out
		}
	}
	for i, item := range items {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(item, &fields); err != nil {
			log.Debugf("content: skipping cPages.pages[%d]: %v", i, err)
			continue
		}
		p := cPage{pos: i}
		_ = json.Unmarshal(fields["id"], &p.id)
		if p.id == "" {
			log.Debugf("content: skipping cPages.pages[%d]: no id", i)
			continue
		}
		if v, ok := decodeLWW(fields["idx"]); ok {
			if json.Unmarshal(v, &p.idx) == nil && p.idx != "" {
				p.hasIdx = true
			}
		}
		if v, ok := decodeLWW(fields["template"]); ok {
			_ = json.Unmarshal(v, &p.template)
		}
		if v, ok := decodeLWW(fields["redir"]); ok {
			if n, ok := toInt(v); ok {
				p.redir = &n
			}
		}
		if v, ok := decodeLWW(fields["deleted"]); ok {
			p.deleted = truthy(v)
		}
		out.pages = append(out.pages, p)
	}
	return out
}

// ordered returns the live pages in document order and whether any entry
// (deleted or not) carried a redir.
//
// Order: when every entry has an idx, entries are sorted by idx.value with
// plain byte-wise string comparison (so a key sorts right after its own
// prefix: "bc" < "bcab" < "bcb" < "bd"), ties keeping array order. If any
// entry lacks an idx the array order is used as is. See testdata/README.md.
func (cp cPages) ordered() ([]Page, bool) {
	entries := append([]cPage(nil), cp.pages...)
	allIdx := len(entries) > 0
	hasRedir := false
	for _, e := range entries {
		if !e.hasIdx {
			allIdx = false
		}
		if e.redir != nil {
			hasRedir = true
		}
	}
	if allIdx {
		sort.SliceStable(entries, func(i, j int) bool { return entries[i].idx < entries[j].idx })
	} else if len(entries) > 0 {
		log.Debug("content: some cPages entries have no idx; using array order")
	}

	pages := make([]Page, 0, len(entries))
	for _, e := range entries {
		if e.deleted {
			continue
		}
		p := Page{ID: e.id, Template: e.template}
		if e.redir != nil && *e.redir >= 0 {
			p.Redir = *e.redir + 1
		}
		pages = append(pages, p)
	}
	return pages, hasRedir
}

// dedupe drops repeated page ids, keeping the first. A page id names a .rm
// file, so the same id twice cannot be two different pages.
func dedupe(pages []Page) []Page {
	seen := make(map[string]bool, len(pages))
	out := pages[:0]
	for _, p := range pages {
		if seen[p.ID] {
			log.Debugf("content: dropping repeated page id %s", p.ID)
			continue
		}
		seen[p.ID] = true
		out = append(out, p)
	}
	return out
}

func attachTags(pages []Page, tags []PageTag) {
	byID := make(map[string]int, len(pages))
	for i, p := range pages {
		byID[p.ID] = i
	}
	for _, t := range tags {
		if i, ok := byID[t.PageID]; ok && t.Name != "" {
			pages[i].Tags = append(pages[i].Tags, t.Name)
		}
	}
}

// decodePageTags accepts both the formatVersion 2 object form
// {"name","pageId","timestamp"} and the older string form, element by
// element.
func decodePageTags(msg json.RawMessage) []PageTag {
	if len(msg) == 0 || isNull(msg) {
		return nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal(msg, &items); err != nil {
		log.Debugf("content: ignoring pageTags: %v", err)
		return nil
	}
	var tags []PageTag
	for i, item := range items {
		var name string
		if json.Unmarshal(item, &name) == nil {
			tags = append(tags, PageTag{Name: name})
			continue
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(item, &fields); err != nil {
			log.Debugf("content: skipping pageTags[%d]: %v", i, err)
			continue
		}
		t := PageTag{}
		_ = json.Unmarshal(fields["name"], &t.Name)
		_ = json.Unmarshal(fields["pageId"], &t.PageID)
		if n, ok := toInt64(fields["timestamp"]); ok {
			t.Timestamp = n
		}
		tags = append(tags, t)
	}
	return tags
}

func isNull(msg json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(msg), []byte("null"))
}

func stringField(raw map[string]json.RawMessage, key string) string {
	var s string
	if msg, ok := raw[key]; ok {
		if err := json.Unmarshal(msg, &s); err != nil {
			log.Debugf("content: ignoring %s: %v", key, err)
		}
	}
	return s
}

func intField(raw map[string]json.RawMessage, key string) (int, bool) {
	msg, ok := raw[key]
	if !ok || isNull(msg) {
		return 0, false
	}
	n, ok := toInt(msg)
	if !ok {
		log.Debugf("content: ignoring %s: not a number", key)
	}
	return n, ok
}

func toInt(msg json.RawMessage) (int, bool) {
	n, ok := toInt64(msg)
	return int(n), ok
}

// toInt64 accepts a JSON number (a fraction is truncated) or a string holding
// one; the device writes some numbers as strings (sizeInBytes, timestamps).
func toInt64(msg json.RawMessage) (int64, bool) {
	if len(msg) == 0 || isNull(msg) {
		return 0, false
	}
	var num json.Number
	if err := json.Unmarshal(msg, &num); err != nil {
		var s string
		if json.Unmarshal(msg, &s) != nil {
			return 0, false
		}
		num = json.Number(strings.TrimSpace(s))
	}
	if n, err := num.Int64(); err == nil {
		return n, true
	}
	if f, err := num.Float64(); err == nil {
		return int64(f), true
	}
	return 0, false
}

// truthy reads a deleted.value: a non-zero number or true.
func truthy(msg json.RawMessage) bool {
	var b bool
	if json.Unmarshal(msg, &b) == nil {
		return b
	}
	n, ok := toInt64(msg)
	return ok && n != 0
}
