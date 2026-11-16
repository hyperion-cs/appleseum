package integration_test

import (
	"database/sql"
	"fmt"
	"net/url"
	"path/filepath"
	"testing"

	"appleseum/backend/internal/wire"
)

func TestCatalogUsesSavedIpaSizesInDisplayAndPaging(t *testing.T) {
	dir := t.TempDir()
	h := launch(t, dir, nil)
	defer h.close()
	h.login(t, "first")
	h.refresh(t)
	db, err := sql.Open("sqlite", filepath.Join(dir, "metadata.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec("INSERT OR IGNORE INTO accounts(id) VALUES('other')"); err != nil {
		t.Fatal(err)
	}
	for i, file := range []struct {
		app, size int64
		owner     string
	}{{1, 100, "first"}, {2, 300, "first"}, {2, 400, "first"}, {4, 200, "other"}} {
		_, err = db.Exec("INSERT INTO packages(id,app_id,name,account_id,bundle,version,size,created_at) VALUES(?,?,?,?,?,?,?,?)", fmt.Sprintf("01900000-0000-7000-8000-%012d", i+1), file.app, "Fixture", file.owner, "test.app", "1", file.size, "2026-11-04T12:00:00Z")
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, direction := range []string{"asc", "desc"} {
		seen := map[string]bool{}
		cursor := ""
		last := int64(-1)
		if direction == "desc" {
			last = 4_611_686_018_427_387_904
		}
		for {
			var page wire.CatalogPage
			h.request(t, "GET", "/api/catalog?limit=3&sort=size&direction="+direction+cursor, nil, 200, &page)
			for _, app := range page.Items {
				if seen[app.Id] {
					t.Fatal("size cursor repeated app", app.Id)
				}
				seen[app.Id] = true
				value := int64(-1)
				if app.Size != nil {
					value = *app.Size
				}
				if direction == "asc" && value < last || direction == "desc" && value > last {
					t.Fatal("display size and sort/cursor disagree", app.Id, value, last)
				}
				last = value
				if expected, ok := map[string]int64{"1": 100, "2": 400, "3": 3_000_000, "4": 200}[app.Id]; ok && value != expected {
					t.Fatal("wrong size source", app.Id, value, expected)
				}
				if app.Id == "6" && app.Size != nil {
					t.Fatal("invented size for unknown delisted app")
				}
			}
			if !page.Next {
				break
			}
			cursor = "&cursor=" + url.QueryEscape(page.Items[len(page.Items)-1].Cursor)
		}
		if len(seen) != 32 {
			t.Fatal("size paging lost apps", len(seen))
		}
	}
	if _, err = db.Exec("DELETE FROM packages WHERE app_id IN (1,2)"); err != nil {
		t.Fatal(err)
	}
	var page wire.CatalogPage
	h.request(t, "GET", "/api/catalog?limit=128", nil, 200, &page)
	for _, app := range page.Items {
		if app.Id == "1" && (app.InLibrary || app.Size == nil || *app.Size != 1_000_000) {
			t.Fatal("deletion did not restore store size", app)
		}
		if app.Id == "2" && (app.InLibrary || app.Size != nil) {
			t.Fatal("deleted IPA size remained", app)
		}
	}
}
