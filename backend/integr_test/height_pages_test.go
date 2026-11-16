package integration_test

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"testing"

	"appleseum/backend/internal/wire"
)

func TestShortenedCatalogPagesResumeAtVisibleRows(t *testing.T) {
	h := launch(t, t.TempDir(), nil)
	defer h.close()
	h.login(t, "height-pages")
	h.refresh(t)
	for _, sort := range []string{"name", "status", "size", "purchasedAt"} {
		for _, direction := range []string{"asc", "desc"} {
			t.Run(sort+"/"+direction, func(t *testing.T) {
				base := fmt.Sprintf("/api/catalog?sort=%s&direction=%s", sort, direction)
				var full, original, next, previous wire.CatalogPage
				h.request(t, "GET", base+"&limit=100", nil, 200, &full)
				h.request(t, "GET", base+"&limit=10", nil, 200, &original)
				if len(original.Items) < 3 {
					t.Fatal("missing row continuations")
				}
				// The client shows only the first three rows of the ten received.
				h.request(t, "GET", base+"&limit=3&cursor="+url.QueryEscape(original.Items[2].Cursor), nil, 200, &next)
				for i, item := range next.Items {
					if item.Id != full.Items[3+i].Id {
						t.Fatal("shortened page skipped a row", item.Id)
					}
				}
				h.request(t, "GET", base+"&limit=3&cursor="+url.QueryEscape(next.Items[0].Cursor)+"&move=prev", nil, 200, &previous)
				for i, item := range previous.Items {
					if item.Id != full.Items[i].Id {
						t.Fatal("backward page lost its boundary", item.Id)
					}
				}
				// The last server page has no Next, but hidden rows still need a continuation.
				h.request(t, "GET", base+"&limit=2&cursor="+url.QueryEscape(full.Items[1].Cursor), nil, 200, &next)
				if next.Items[0].Id != full.Items[2].Id {
					t.Fatal("shortening the last page skipped hidden rows")
				}
			})
		}
	}
	for _, resource := range []string{"catalog", "library", "shares"} {
		h.request(t, "GET", "/api/"+resource+"?limit=128", nil, 200, nil)
		for _, limit := range []int{129, 999} {
			h.request(t, "GET", fmt.Sprintf("/api/%s?limit=%d", resource, limit), nil, 400, nil)
		}
	}
	h.request(t, "POST", "/api/catalog/1/versions", wire.VersionPageRequest{Limit: 129}, 400, nil)
}

func TestDeletedCursorBoundaryDoesNotSkipRows(t *testing.T) {
	dir := t.TempDir()
	h := launch(t, dir, nil)
	defer h.close()
	h.login(t, "cursor-removal")
	h.refresh(t)
	var first, next wire.CatalogPage
	h.request(t, "GET", "/api/catalog?limit=10&sort=name&direction=asc", nil, 200, &first)
	db := deviceDb(t, dir)
	// Delete by the page's internal unique ID, independently of account naming.
	raw, err := base64.RawURLEncoding.DecodeString(first.Items[2].Cursor)
	var pair []any
	if err != nil || json.Unmarshal(raw, &pair) != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("DELETE FROM catalog WHERE id=?", pair[1]); err != nil {
		t.Fatal(err)
	}
	h.request(t, "GET", "/api/catalog?limit=3&sort=name&direction=asc&cursor="+url.QueryEscape(first.Items[2].Cursor)+"&move=next", nil, 200, &next)
	if len(next.Items) != 3 || next.Items[0].Id != first.Items[3].Id {
		t.Fatal("deleted anchor skipped or repeated rows", next.Items)
	}
}
