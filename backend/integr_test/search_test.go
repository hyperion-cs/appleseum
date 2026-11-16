package integration_test

import (
	"net/url"
	"testing"

	"appleseum/backend/internal/wire"
)

func TestSearchMinimumAcrossHttpRoutes(t *testing.T) {
	h := launch(t, t.TempDir(), nil)
	defer h.close()
	h.login(t, "first")
	h.refresh(t)
	for _, query := range []string{"a", "ab", "я", "ёж", "🙂", "🙂🙂"} {
		for _, path := range []string{"/api/catalog", "/api/library", "/api/shares"} {
			h.request(t, "GET", path+"?q="+url.QueryEscape(query), nil, 400, nil)
		}
		for _, resource := range []wire.Table{wire.CatalogTable, wire.LibraryTable} {
			h.request(t, "POST", "/api/"+string(resource)+"/selection", wire.Selection{Filter: &wire.Filter{Query: query}}, 400, nil)
		}
		for _, path := range []string{"/api/shares/selection", "/api/shares/stop", "/api/shares", "/api/library/delete", "/api/library/export", "/api/downloads"} {
			h.request(t, "POST", path, wire.Selection{Filter: &wire.Filter{Query: query}}, 400, nil)
		}
	}
	var page wire.CatalogPage
	h.request(t, "GET", "/api/catalog?q=App%2001", nil, 200, &page)
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].Id != "1" {
		t.Fatalf("indexed catalog search: %+v", page)
	}
	var selected wire.SelectionResult
	h.request(t, "POST", "/api/catalog/selection", wire.Selection{Filter: &wire.Filter{Query: "App 01"}}, 200, &selected)
	if len(selected.Ids) != 1 || selected.Ids[0] != "1" {
		t.Fatalf("indexed catalog selection: %+v", selected)
	}
}
