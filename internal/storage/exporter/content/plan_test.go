package content

import (
	"reflect"
	"testing"
)

func TestBuildPlan(t *testing.T) {
	const v2p = "e5000005-3a9d-4b6c-8d1e-2f7a8b00000"
	const v1p = "b2000002-1c4e-4d77-8f0a-6a9e0b00000"
	tests := []struct {
		name        string
		file        string // fixture, or
		json        string // inline .content
		rm          map[string]Format
		landscape   bool
		payloadType string
		pages       []PlanPage
	}{
		{
			name: "pdf with inserted and deleted pages",
			file: "v2_pdf_inserted_deleted.content",
			rm: map[string]Format{
				v2p + "1":    FormatV6,
				v2p + "2":    FormatV6, // blob of the deleted page: ignored
				v2p + "5":    FormatV5,
				"stray-0000": FormatV6, // not in the content: ignored
			},
			landscape: true, payloadType: "pdf",
			pages: []PlanPage{
				{ID: v2p + "1", Format: FormatV6, HasInk: true, Background: 1, Template: "Blank"},
				{ID: v2p + "5", Format: FormatV5, HasInk: true, Background: 0, Template: "LS Lines medium"},
				{ID: v2p + "3", Background: 3, Template: "Blank"},
				{ID: v2p + "4", Background: 4, Template: "Blank"},
			},
		},
		{
			name:        "v1 pdf keeps its explicit inserted page",
			file:        "v1_pdf_redirection.content",
			rm:          map[string]Format{v1p + "3": FormatV5},
			payloadType: "pdf",
			pages: []PlanPage{
				{ID: v1p + "1", Background: 1}, {ID: v1p + "2", Background: 2},
				{ID: v1p + "3", Format: FormatV5, HasInk: true},
				{ID: v1p + "4", Background: 3}, {ID: v1p + "5", Background: 4}, {ID: v1p + "6", Background: 5},
			},
		},
		{
			name: "notebook passes a stated redirection through",
			json: `{"fileType":"notebook","pages":["a","b"],"redirectionPageMap":[0,1]}`,
			rm:   map[string]Format{"b": FormatV6},
			pages: []PlanPage{
				{ID: "a", Background: 1},
				{ID: "b", Format: FormatV6, HasInk: true, Background: 2},
			},
		},
		{
			name:        "pdf without any redirection pairs page i with PDF page i",
			json:        `{"fileType":"pdf","formatVersion":1,"pageCount":3,"pages":["a","b","c"]}`,
			payloadType: "pdf",
			pages:       []PlanPage{{ID: "a", Background: 1}, {ID: "b", Background: 2}, {ID: "c", Background: 3}},
		},
		{
			name:        "identity fallback also for cPages without redir",
			json:        `{"fileType":"pdf","cPages":{"original":{"timestamp":"1:1","value":2},"pages":[{"id":"a","idx":{"timestamp":"1:2","value":"ba"}},{"id":"b","idx":{"timestamp":"1:2","value":"bb"}}]}}`,
			payloadType: "pdf",
			pages:       []PlanPage{{ID: "a", Background: 1}, {ID: "b", Background: 2}},
		},
		{
			name:        "more pages than the payload: pair up to the payload's count",
			json:        `{"fileType":"pdf","originalPageCount":2,"pageCount":3,"pages":["a","b","c"]}`,
			payloadType: "pdf",
			pages:       []PlanPage{{ID: "a", Background: 1}, {ID: "b", Background: 2}, {ID: "c"}},
		},
		{
			name:        "fewer pages than the payload: pair every page",
			json:        `{"fileType":"pdf","pageCount":4,"pages":["a","b"]}`,
			payloadType: "pdf",
			pages:       []PlanPage{{ID: "a", Background: 1}, {ID: "b", Background: 2}},
		},
		{
			name:        "no identity fallback when the map says every page is inserted",
			json:        `{"fileType":"pdf","pageCount":2,"pages":["a","b"],"redirectionPageMap":[-1,-1]}`,
			payloadType: "pdf",
			pages:       []PlanPage{{ID: "a"}, {ID: "b"}},
		},
		{
			name:        "no identity fallback for epub",
			json:        `{"fileType":"epub","pageCount":2,"pages":["a","b"]}`,
			payloadType: "epub",
			pages:       []PlanPage{{ID: "a"}, {ID: "b"}},
		},
		{
			name:        "blob with unknown header still counts as ink",
			json:        `{"fileType":"pdf","pages":["a"],"redirectionPageMap":[0]}`,
			rm:          map[string]Format{"a": FormatUnknown},
			payloadType: "pdf",
			pages:       []PlanPage{{ID: "a", HasInk: true, Background: 1}},
		},
		{
			name:  "empty document",
			json:  `{"fileType":"notebook"}`,
			pages: []PlanPage{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := []byte(tt.json)
			if tt.file != "" {
				data = readFixture(t, tt.file)
			}
			c, err := Parse(data)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			plan, err := BuildPlan(c, tt.rm)
			if err != nil {
				t.Fatalf("BuildPlan: %v", err)
			}
			if plan.Landscape != tt.landscape || plan.PayloadType != tt.payloadType {
				t.Errorf("Landscape=%v PayloadType=%q, want %v %q", plan.Landscape, plan.PayloadType, tt.landscape, tt.payloadType)
			}
			if !reflect.DeepEqual(plan.Pages, tt.pages) {
				t.Errorf("pages\n got %+v\nwant %+v", plan.Pages, tt.pages)
			}
		})
	}
}

func TestBuildPlanNil(t *testing.T) {
	if _, err := BuildPlan(nil, nil); err == nil {
		t.Error("BuildPlan(nil) succeeded")
	}
}

// TestBuildPlanNoPageList: a file with no page list gives an empty plan, not
// a crash, and Parse's error tells the caller why.
func TestBuildPlanNoPageList(t *testing.T) {
	c, err := Parse([]byte(`{"fileType":"pdf","pageCount":4}`))
	if err == nil {
		t.Fatal("want ErrNoPageList")
	}
	plan, err := BuildPlan(c, map[string]Format{"x": FormatV6})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Pages) != 0 {
		t.Errorf("pages = %+v", plan.Pages)
	}
}
