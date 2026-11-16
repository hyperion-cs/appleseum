package integration_test

import (
	"testing"

	"appleseum/backend/internal/wire"
)

func TestCatalogDefaultSortingAndRefresh(t *testing.T) {
	h := launch(t, t.TempDir(), nil)
	defer h.close()
	var state wire.State
	h.request(t, "GET", "/api/state", nil, 200, &state)
	if want := (wire.CatalogView{Sort: "purchasedAt", Direction: "desc", Delisted: true}); state.Tables.Catalog.Value != want {
		t.Fatalf("new workspace has wrong defaults: %+v", state.Tables.Catalog)
	}
	h.login(t, "metadata")
	h.refresh(t)
	input := wire.CatalogView{Sort: "name", Direction: "asc", Delisted: true}
	var saved wire.Saved[wire.CatalogView]
	h.request(t, "PUT", "/api/table-settings/catalog", input, 200, &saved)
	h.request(t, "GET", "/api/state", nil, 200, &state)
	library := state.Tables.Library
	h.refresh(t)
	h.request(t, "GET", "/api/state", nil, 200, &state)
	if got := state.Tables.Catalog; got.Value.Sort != "purchasedAt" || got.Value.Direction != "desc" || !got.Value.Delisted || got.Revision <= saved.Revision {
		t.Fatalf("refresh did not reset sorting while preserving filters: %+v", got)
	}
	if state.Tables.Library != library {
		t.Fatal("catalog refresh changed library settings")
	}
	var page wire.CatalogPage
	h.request(t, "GET", "/api/catalog?limit=2", nil, 200, &page)
	if len(page.Items) != 2 || page.Items[0].PurchasedAt != "2026-01-28T12:00:00Z" || page.Items[1].PurchasedAt != "2026-01-27T12:00:00Z" {
		t.Fatalf("default API order is not newest purchases first: %+v", page)
	}
	h.request(t, "GET", "/api/catalog?limit=2&sort=name&direction=asc", nil, 200, &page)
	if page.Items[0].Name != "App 01" || page.Items[1].Name != "App 02" {
		t.Fatal("explicit alphabetical sorting no longer works")
	}
}

func TestPublicCatalogDefaultsAndRefreshAreSessionScoped(t *testing.T) {
	_, ts, _ := publicApp(t)
	one, two := newBrowser(t, ts), newBrowser(t, ts)
	for _, b := range []*browser{one, two} {
		if value := b.state().Tables.Catalog.Value; value != (wire.CatalogView{Sort: "purchasedAt", Direction: "desc", Delisted: true}) {
			t.Fatalf("new browser has wrong defaults: %+v", value)
		}
		b.login("same@example.test", "password-marker")
		b.request("PUT", "/api/table-settings/catalog", wire.CatalogView{Sort: "name", Direction: "asc"}, 200, nil)
	}
	one.refreshCatalog()
	if value := one.state().Tables.Catalog.Value; value.Sort != "purchasedAt" || value.Direction != "desc" {
		t.Fatalf("refresh did not reset browser sorting: %+v", value)
	}
	if value := two.state().Tables.Catalog.Value; value.Sort != "name" || value.Direction != "asc" {
		t.Fatal("refresh changed another browser's chosen sorting")
	}
}
