package debuglog

import (
	"net/url"
	"slices"
	"testing"

	"appleseum/backend/internal/wire"
)

func debugPage(events []wire.DebugEvent, query url.Values) ([]wire.DebugEvent, int, bool, bool, error) {
	filter, page, err := ParseQuery(query)
	if err != nil {
		return nil, 0, false, false, err
	}
	return Page(events, filter, page)
}

func TestDebugPageEventTypes(t *testing.T) {
	events := []wire.DebugEvent{
		{Id: 1, Source: "Apple", Target: "example.test /download"},
		{Id: 2, Source: "API", Target: "/api/login"},
		{Id: 3, Source: "API", Target: "/api/shares", Status: 201},
		{Id: 4, Source: "API", Target: "/api/shares/{id}", Status: 204},
		{Id: 5, Source: "Hosting", Target: "/manifests/{share}.plist"},
		{Id: 6, Source: "Hosting", Target: "/packages/{share}/{file}.ipa"},
		{Id: 7, Source: "System", Target: "storage", Detail: "write_failed"},
		{Id: 8, Source: "Auth", Target: "sign_in", Detail: "legacy_error"},
		{Id: 9, Source: "API", Target: "/api/shares-other"},
		{Id: 10, Source: "Apple", Target: "example.test /api/shares"},
	}
	for _, test := range []struct {
		name    string
		sources []string
		search  string
		ids     []int64
	}{
		{"apple", []string{"Apple"}, "", []int64{10, 1}},
		{"api", []string{"API"}, "", []int64{9, 4, 3, 2}},
		{"hosting", []string{"Hosting"}, "", []int64{6, 5, 4, 3}},
		{"system", []string{"System"}, "", []int64{8, 7}},
		{"overlapping", []string{"API", "Hosting", "API"}, "", []int64{9, 6, 5, 4, 3, 2}},
		{"search", []string{"API", "Hosting"}, "204", []int64{4}},
		{"all", []string{"Apple", "API", "Hosting", "System"}, "", []int64{10, 9, 8, 7, 6, 5, 4, 3, 2, 1}},
	} {
		t.Run(test.name, func(t *testing.T) {
			query := url.Values{"source": test.sources, "q": {test.search}, "limit": {"2"}}
			var ids []int64
			for {
				page, total, next, _, err := debugPage(events, query)
				if err != nil || total != len(test.ids) {
					t.Fatal("wrong filtered total", total, err)
				}
				for _, event := range page {
					ids = append(ids, event.Id)
				}
				if !next {
					break
				}
				if len(ids) >= len(events) {
					t.Fatal("pagination did not advance")
				}
				query.Set("cursor", page[len(page)-1].Cursor)
			}
			if !slices.Equal(ids, test.ids) {
				t.Fatal("missing or duplicated events", ids)
			}
		})
	}
}

func TestDebugPageMultipleSources(t *testing.T) {
	events := []wire.DebugEvent{
		{Id: 1, Source: "Apple", Target: "/login"},
		{Id: 2, Source: "Hosting", Target: "/packages"},
		{Id: 3, Source: "API", Target: "/login"},
		{Id: 4, Source: "System", Detail: "error"},
		{Id: 5, Source: "Apple", Target: "/lookup"},
	}
	query := url.Values{"source": {"Apple", "API"}, "limit": {"2"}}
	page, total, next, previous, err := debugPage(events, query)
	if err != nil || total != 3 || len(page) != 2 || page[0].Id != 5 || page[1].Id != 3 || !next || previous {
		t.Fatal("wrong first page", page, total, err)
	}
	// Equivalent sets share a cursor, regardless of order or duplicate values.
	query["source"] = []string{"API", "Apple", "API"}
	query.Set("cursor", page[len(page)-1].Cursor)
	page, total, next, previous, err = debugPage(events, query)
	if err != nil || total != 3 || len(page) != 1 || page[0].Id != 1 || next || !previous {
		t.Fatal("wrong next page", page, total, err)
	}
	query.Set("cursor", page[0].Cursor)
	query.Set("move", "prev")
	page, _, _, _, err = debugPage(events, query)
	if err != nil || len(page) != 2 || page[0].Id != 5 || page[1].Id != 3 {
		t.Fatal("wrong previous page", page, err)
	}
	query["source"] = []string{"Apple", "Hosting"}
	if _, _, _, _, err := debugPage(events, query); err != nil {
		t.Fatal("cursor should use the supplied filters", err)
	}
	query.Del("move")
	query.Del("cursor")
	query["source"] = []string{"Apple", "API"}
	query.Set("q", "LOGIN")
	page, total, _, _, err = debugPage(events, query)
	if err != nil || total != 2 || len(page) != 2 || page[0].Id != 3 || page[1].Id != 1 {
		t.Fatal("search did not intersect the source union", page, total, err)
	}
	if !slices.Equal(query["source"], []string{"Apple", "API"}) {
		t.Fatal("query was modified")
	}
	for _, filter := range []url.Values{nil, {"source": {""}}} {
		_, total, _, _, err := debugPage(events, filter)
		if err != nil || total != len(events) {
			t.Fatal("unfiltered view changed", total, err)
		}
	}
	for _, invalid := range []url.Values{
		{"source": {"Apple", "unknown"}},
		{"source": {"", "API"}},
		{"source": {}},
		{"limit": {"1", "2"}},
		{"q": {"one", "two"}},
	} {
		if _, _, _, _, err := debugPage(events, invalid); err == nil {
			t.Fatal("invalid query accepted", invalid)
		}
	}
}

func TestDebugShortenedPageAndSizeLimit(t *testing.T) {
	events := []wire.DebugEvent{{Id: 1}, {Id: 2}, {Id: 3}, {Id: 4}}
	query := url.Values{"limit": {"128"}}
	page, _, next, _, err := debugPage(events, query)
	if err != nil || len(page) != 4 || next {
		t.Fatal("missing last-page continuations", err)
	}
	query.Set("limit", "1")
	query.Set("cursor", page[1].Cursor)
	page, _, _, _, err = debugPage(events, query)
	if err != nil || len(page) != 1 || page[0].Id != 2 {
		t.Fatal("shortened page skipped events", page, err)
	}
	query = url.Values{"limit": {"129"}}
	if _, _, _, _, err = debugPage(events, query); err == nil {
		t.Fatal("accepted more than 128 rows")
	}
}
