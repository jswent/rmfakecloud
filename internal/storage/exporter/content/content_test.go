package content

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// want is a compact description of one parsed page.
type want struct {
	ID       string
	Redir    int
	Template string
	Tags     []string
}

func pagesOf(c *Content) []want {
	out := make([]want, 0, len(c.Pages))
	for i, p := range c.Pages {
		if p.Index != i {
			panic("Index not set")
		}
		out = append(out, want{ID: p.ID, Redir: p.Redir, Template: p.Template, Tags: p.Tags})
	}
	return out
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParseFixtures(t *testing.T) {
	const (
		v1n = "a1000001-5d0c-4e1a-9b51-3f2c7a00000"
		v1p = "b2000002-1c4e-4d77-8f0a-6a9e0b00000"
		v2n = "c3000003-7b21-4f8e-a2d4-90c1d500000"
		v2p = "e5000005-3a9d-4b6c-8d1e-2f7a8b00000"
		v2t = "f6000006-8e2b-4a1f-b3c5-7d9e0f00000"
	)
	tests := []struct {
		file           string
		formatVersion  int
		fileType       string
		orientation    string
		pageCount      int
		coverPage      int
		original       int
		hasRedirection bool
		source         PageSource
		pages          []want
	}{
		{
			file: "v1_notebook.content", formatVersion: 1, fileType: "notebook", orientation: "portrait",
			pageCount: 3, source: SourcePages,
			pages: []want{{ID: v1n + "1"}, {ID: v1n + "2"}, {ID: v1n + "3"}},
		},
		{
			// redirectionPageMap [0, 1, -1, 2, 3, 4]: the third page was
			// inserted by hand and has no PDF page under it.
			file: "v1_pdf_redirection.content", formatVersion: 1, fileType: "pdf", orientation: "portrait",
			pageCount: 6, coverPage: -1, original: 5, hasRedirection: true, source: SourcePages,
			pages: []want{
				{ID: v1p + "1", Redir: 1}, {ID: v1p + "2", Redir: 2}, {ID: v1p + "3", Redir: 0},
				{ID: v1p + "4", Redir: 3}, {ID: v1p + "5", Redir: 4}, {ID: v1p + "6", Redir: 5},
			},
		},
		{
			// Array order is ba bb bc bbb bcab; idx order is ba bb bbb bc bcab.
			file: "v2_notebook_paperpro.content", formatVersion: 2, fileType: "notebook", orientation: "portrait",
			pageCount: 5, source: SourceCPages,
			pages: []want{
				{ID: v2n + "1", Template: "P Grid medium"},
				{ID: v2n + "2", Template: "P Grid medium"},
				{ID: v2n + "4", Template: "Blank"},
				{ID: v2n + "3", Template: "P Lines small"},
				{ID: v2n + "5", Template: "P Lines small"},
			},
		},
		{
			// PDF pages 1..4 as ba bb bc bd; bb (PDF page 2) deleted; an
			// inserted page bab after page 1; an inserted page bcb, deleted.
			file: "v2_pdf_inserted_deleted.content", formatVersion: 2, fileType: "pdf", orientation: "landscape",
			pageCount: 4, coverPage: -1, original: 4, hasRedirection: true, source: SourceCPages,
			pages: []want{
				{ID: v2p + "1", Redir: 1, Template: "Blank"},
				{ID: v2p + "5", Redir: 0, Template: "LS Lines medium"},
				{ID: v2p + "3", Redir: 3, Template: "Blank"},
				{ID: v2p + "4", Redir: 4, Template: "Blank"},
			},
		},
		{
			file: "v2_pagetags.content", formatVersion: 2, fileType: "notebook", orientation: "portrait",
			pageCount: 2, source: SourceCPages,
			pages: []want{
				{ID: v2t + "1", Template: "P Lines medium", Tags: []string{"todo", "meeting"}},
				{ID: v2t + "2", Template: "P Lines medium", Tags: []string{"todo"}},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			c, err := Parse(readFixture(t, tt.file))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if c.FormatVersion != tt.formatVersion || c.FileType != tt.fileType || c.Orientation != tt.orientation ||
				c.PageCount != tt.pageCount || c.CoverPage != tt.coverPage || c.OriginalPageCount != tt.original ||
				c.HasRedirection != tt.hasRedirection || c.Source != tt.source {
				t.Errorf("header fields = %+v", *c)
			}
			if got := pagesOf(c); !reflect.DeepEqual(got, tt.pages) {
				t.Errorf("pages\n got %+v\nwant %+v", got, tt.pages)
			}
		})
	}
}

func TestParsePageTagsKeepsAll(t *testing.T) {
	c, err := Parse(readFixture(t, "v2_pagetags.content"))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.PageTags) != 4 {
		t.Fatalf("PageTags = %+v", c.PageTags)
	}
	last := c.PageTags[3]
	if last.Name != "gone" || last.PageID != "f6000006-8e2b-4a1f-b3c5-7d9e0f000003" || last.Timestamp != 1773480003000 {
		t.Errorf("PageTags[3] = %+v", last)
	}
}

func TestParseLastOpened(t *testing.T) {
	c, err := Parse(readFixture(t, "v2_notebook_paperpro.content"))
	if err != nil {
		t.Fatal(err)
	}
	if c.LastOpenedPageID != "c3000003-7b21-4f8e-a2d4-90c1d5000005" {
		t.Errorf("LastOpenedPageID = %q", c.LastOpenedPageID)
	}
	if c.OriginalPageCount != 0 {
		t.Errorf("OriginalPageCount = %d, want 0 for a notebook (-1 on disk)", c.OriginalPageCount)
	}
}

func TestApplyPagedata(t *testing.T) {
	c, err := Parse(readFixture(t, "v1_notebook.content"))
	if err != nil {
		t.Fatal(err)
	}
	ApplyPagedata(c, readFixture(t, "v1_notebook.pagedata"))
	var got []string
	for _, p := range c.Pages {
		got = append(got, p.Template)
	}
	if want := []string{"P Lines small", "P Grid medium", "Blank"}; !reflect.DeepEqual(got, want) {
		t.Errorf("templates = %q, want %q", got, want)
	}
	ApplyPagedata(nil, nil) // must not panic
}

func TestParseEdgeCases(t *testing.T) {
	tests := []struct {
		name    string
		json    string
		pages   []want
		source  PageSource
		noPages bool // expect ErrNoPageList
	}{
		{
			name:  "empty object",
			json:  `{}`,
			pages: []want{},
		},
		{
			name:    "pageCount without any page list",
			json:    `{"fileType":"pdf","formatVersion":2,"pageCount":3}`,
			pages:   []want{},
			noPages: true,
		},
		{
			name:    "null lists",
			json:    `{"pageCount":2,"pages":null,"cPages":null,"redirectionPageMap":null}`,
			pages:   []want{},
			noPages: true,
		},
		{
			name:    "cPages without pages",
			json:    `{"pageCount":1,"cPages":{"lastOpened":{"timestamp":"1:1","value":""}}}`,
			pages:   []want{},
			noPages: true,
		},
		{
			name:   "formatVersion 2 prefers cPages over a stale flat list",
			json:   `{"formatVersion":2,"pages":["old"],"cPages":{"pages":[{"id":"new","idx":{"timestamp":"1:2","value":"ba"}}]}}`,
			pages:  []want{{ID: "new"}},
			source: SourceCPages,
		},
		{
			name:   "formatVersion 1 prefers the flat list",
			json:   `{"formatVersion":1,"pages":["old"],"cPages":{"pages":[{"id":"new","idx":{"timestamp":"1:2","value":"ba"}}]}}`,
			pages:  []want{{ID: "old"}},
			source: SourcePages,
		},
		{
			name:   "no formatVersion prefers cPages",
			json:   `{"pages":["old"],"cPages":{"pages":[{"id":"new","idx":{"timestamp":"1:2","value":"ba"}}]}}`,
			pages:  []want{{ID: "new"}},
			source: SourceCPages,
		},
		{
			name:   "formatVersion 2 with only a flat list falls back to it",
			json:   `{"formatVersion":2,"pages":["a","b"]}`,
			pages:  []want{{ID: "a"}, {ID: "b"}},
			source: SourcePages,
		},
		{
			name:   "missing idx falls back to array order",
			json:   `{"cPages":{"pages":[{"id":"x","idx":{"timestamp":"1:2","value":"bc"}},{"id":"y"},{"id":"z","idx":{"timestamp":"1:2","value":"ba"}}]}}`,
			pages:  []want{{ID: "x"}, {ID: "y"}, {ID: "z"}},
			source: SourceCPages,
		},
		{
			name: "idx is byte-wise lexicographic, prefix first",
			json: `{"cPages":{"pages":[
				{"id":"5","idx":{"timestamp":"1:2","value":"c"}},
				{"id":"3","idx":{"timestamp":"1:2","value":"bcab"}},
				{"id":"1","idx":{"timestamp":"1:2","value":"bb"}},
				{"id":"4","idx":{"timestamp":"1:2","value":"bcb"}},
				{"id":"2","idx":{"timestamp":"1:2","value":"bc"}},
				{"id":"6","idx":{"timestamp":"1:2","value":"nna"}}]}}`,
			pages:  []want{{ID: "1"}, {ID: "2"}, {ID: "3"}, {ID: "4"}, {ID: "5"}, {ID: "6"}},
			source: SourceCPages,
		},
		{
			name:   "equal idx keeps array order",
			json:   `{"cPages":{"pages":[{"id":"b","idx":{"timestamp":"2:2","value":"ba"}},{"id":"a","idx":{"timestamp":"1:2","value":"ba"}}]}}`,
			pages:  []want{{ID: "b"}, {ID: "a"}},
			source: SourceCPages,
		},
		{
			name: "deleted value forms",
			json: `{"cPages":{"pages":[
				{"id":"keep0","idx":{"timestamp":"1:2","value":"ba"},"deleted":{"timestamp":"1:3","value":0}},
				{"id":"del1","idx":{"timestamp":"1:2","value":"bb"},"deleted":{"timestamp":"1:3","value":1}},
				{"id":"delTrue","idx":{"timestamp":"1:2","value":"bc"},"deleted":{"timestamp":"1:3","value":true}},
				{"id":"keepNull","idx":{"timestamp":"1:2","value":"bd"},"deleted":null}]}}`,
			pages:  []want{{ID: "keep0"}, {ID: "keepNull"}},
			source: SourceCPages,
		},
		{
			name:   "redirectionPageMap shorter than pages",
			json:   `{"fileType":"pdf","pages":["a","b","c"],"redirectionPageMap":[0,1]}`,
			pages:  []want{{ID: "a", Redir: 1}, {ID: "b", Redir: 2}, {ID: "c"}},
			source: SourcePages,
		},
		{
			name:   "redirectionPageMap with junk entry",
			json:   `{"fileType":"pdf","pages":["a","b"],"redirectionPageMap":["x",1]}`,
			pages:  []want{{ID: "a"}, {ID: "b", Redir: 2}},
			source: SourcePages,
		},
		{
			name:   "non-string page ids are skipped",
			json:   `{"pages":["a",7,"",null,"b"],"redirectionPageMap":[0,1,2,3,4]}`,
			pages:  []want{{ID: "a", Redir: 1}, {ID: "b", Redir: 5}},
			source: SourcePages,
		},
		{
			name:   "repeated page id is kept once",
			json:   `{"cPages":{"pages":[{"id":"a","idx":{"timestamp":"1:2","value":"ba"}},{"id":"a","idx":{"timestamp":"1:3","value":"bb"}}]}}`,
			pages:  []want{{ID: "a"}},
			source: SourceCPages,
		},
		{
			name:   "cPages entry without id is skipped",
			json:   `{"cPages":{"pages":[{"idx":{"timestamp":"1:2","value":"ba"}},{"id":"b","idx":{"timestamp":"1:2","value":"bb"}},"junk"]}}`,
			pages:  []want{{ID: "b"}},
			source: SourceCPages,
		},
		{
			name:   "negative redir is no background",
			json:   `{"cPages":{"pages":[{"id":"a","idx":{"timestamp":"1:2","value":"ba"},"redir":{"timestamp":"1:2","value":-1}}]}}`,
			pages:  []want{{ID: "a"}},
			source: SourceCPages,
		},
		{
			name:   "string pageTags in a formatVersion 2 file",
			json:   `{"formatVersion":2,"pageTags":["old"],"cPages":{"pages":[{"id":"a","idx":{"timestamp":"1:2","value":"ba"}}]}}`,
			pages:  []want{{ID: "a"}},
			source: SourceCPages,
		},
		{
			name: "odd field types do not drop the page list",
			json: `{"formatVersion":"2","pageCount":"1","lastOpenedPage":1.5,"margins":"wide","lineHeight":1.5,
				"textScale":"big","transform":{"m11":0.5},"pageTags":{"not":"a list"},"orientation":7,
				"customZoomScale":"x","extraMetadata":[],"documentMetadata":"",
				"cPages":{"original":{"timestamp":"1:1","value":"3"},"pages":[{"id":"a","idx":{"timestamp":"1:2","value":"ba"},
				"template":{"timestamp":"1:1","value":3},"redir":{"timestamp":"1:2","value":2.0},
				"scrollTime":{"timestamp":"1:9","value":12},"verticalScroll":"x","modifed":17}],"uuids":null}}`,
			pages:  []want{{ID: "a", Redir: 3}},
			source: SourceCPages,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := Parse([]byte(tt.json))
			if tt.noPages {
				if !errors.Is(err, ErrNoPageList) {
					t.Fatalf("err = %v, want ErrNoPageList", err)
				}
				var npe *NoPageListError
				if !errors.As(err, &npe) || npe.PageCount <= 0 {
					t.Errorf("err = %#v, want *NoPageListError with PageCount", err)
				}
				if c == nil {
					t.Fatal("Content must be returned alongside ErrNoPageList")
				}
			} else if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if got := pagesOf(c); !reflect.DeepEqual(got, tt.pages) {
				t.Errorf("pages\n got %+v\nwant %+v", got, tt.pages)
			}
			if c.Source != tt.source {
				t.Errorf("Source = %v, want %v", c.Source, tt.source)
			}
		})
	}
}

func TestParseRejectsNonObjects(t *testing.T) {
	for _, in := range []string{``, `null`, `[]`, `"x"`, `{`, `{"pages":[}`} {
		if c, err := Parse([]byte(in)); err == nil {
			t.Errorf("Parse(%q) = %+v, want error", in, c)
		}
	}
}

func TestParseOddFieldValues(t *testing.T) {
	c, err := Parse([]byte(`{"formatVersion":"2","pageCount":"1","orientation":" Landscape ","fileType":"PDF",
		"coverPageNumber":-1,"cPages":{"original":{"timestamp":"1:1","value":"3"},
		"pages":[{"id":"a","idx":{"timestamp":"1:2","value":"ba"}}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if c.FormatVersion != 2 || c.PageCount != 1 || c.Orientation != "landscape" || c.FileType != "pdf" ||
		c.CoverPage != -1 || c.OriginalPageCount != 3 || c.HasRedirection {
		t.Errorf("got %+v", *c)
	}
}
