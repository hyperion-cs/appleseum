package integration_test

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"appleseum/backend/internal/wire"
)

func requireAppCopies(t *testing.T, dir string, appId int64, files, jobs int) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "metadata.db")+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var gotFiles, gotJobs int
	if err := db.QueryRow("SELECT (SELECT count(*) FROM packages WHERE size IS NOT NULL AND app_id=?),(SELECT count(*) FROM downloads d JOIN packages p ON p.id=d.package_id WHERE p.app_id=?)", appId, appId).Scan(&gotFiles, &gotJobs); err != nil {
		t.Fatal(err)
	}
	if gotFiles != files || gotJobs != jobs {
		t.Fatalf("app %d: files/jobs=%d/%d, want %d/%d", appId, gotFiles, gotJobs, files, jobs)
	}
}

func TestDownloadsSkipSavedAppsAcrossAccounts(t *testing.T) {
	dir := t.TempDir()
	h := launch(t, dir, nil)
	defer h.close()
	h.login(t, "first")
	h.refresh(t)
	h.request(t, "POST", "/api/downloads", wire.DownloadRequest{Selection: wire.Selection{Ids: []string{"1"}}}, 202, nil)
	h.waitDone(t, 1)
	for range 2 {
		h.request(t, "POST", "/api/downloads", wire.DownloadRequest{Selection: wire.Selection{Ids: []string{"1"}}}, 202, nil)
		requireAppCopies(t, dir, 1, 1, 1)
	}
	h.request(t, "POST", "/api/downloads", wire.DownloadRequest{Selection: wire.Selection{Ids: []string{"1", "2"}}}, 202, nil)
	h.waitDone(t, 2)
	requireAppCopies(t, dir, 1, 1, 1)
	requireAppCopies(t, dir, 2, 1, 1)
	h.request(t, "POST", "/api/logout", nil, 204, nil)
	h.login(t, "second")
	h.refresh(t)
	h.request(t, "POST", "/api/downloads", wire.DownloadRequest{Selection: wire.Selection{Ids: []string{"1", "3"}}}, 202, nil)
	h.waitDone(t, 1)
	requireAppCopies(t, dir, 1, 1, 1)
	requireAppCopies(t, dir, 3, 1, 1)
	var library wire.LibraryPage
	h.request(t, "GET", "/api/library", nil, 200, &library)
	if library.Total != 3 {
		t.Fatal("duplicate created in the shared library")
	}
	for _, file := range library.Items {
		if file.AppId == "1" {
			h.request(t, "POST", "/api/library/delete", wire.Selection{Ids: []string{file.Id}}, 204, nil)
		}
	}
	h.request(t, "POST", "/api/downloads", wire.DownloadRequest{Selection: wire.Selection{Ids: []string{"1"}}}, 202, nil)
	h.waitDone(t, 2)
	requireAppCopies(t, dir, 1, 1, 1) // Removing the IPA permits a fresh download.
}

func TestCatalogSelectionExcludesSavedAndActiveApps(t *testing.T) {
	dir := t.TempDir()
	h := launch(t, dir, nil)
	defer h.close()
	h.login(t, "first")
	h.refresh(t)
	h.request(t, "POST", "/api/downloads", wire.DownloadRequest{Selection: wire.Selection{Ids: []string{"1"}}}, 202, nil)
	h.waitDone(t, 1)
	var selected wire.SelectionResult
	h.request(t, "POST", "/api/catalog/selection", wire.Selection{Ids: []string{"1", "2"}}, 200, &selected)
	if len(selected.Ids) != 1 || selected.Ids[0] != "2" {
		t.Fatal("stale selected ID was not removed", selected)
	}
	h.request(t, "POST", "/api/catalog/selection", wire.Selection{Filter: &wire.Filter{Query: "App 0"}}, 200, &selected)
	if len(selected.Ids) != 8 {
		t.Fatal("select-all included a saved IPA", selected)
	}
	h.request(t, "POST", "/api/downloads", wire.DownloadRequest{Selection: wire.Selection{Filter: &wire.Filter{Query: "App 0"}}}, 202, nil)
	h.waitDone(t, 9)
	requireAppCopies(t, dir, 1, 1, 1)
	h.request(t, "POST", "/api/downloads", wire.DownloadRequest{Selection: wire.Selection{Ids: []string{"32"}}}, 202, nil)
	h.request(t, "POST", "/api/catalog/selection", wire.Selection{Ids: []string{"32"}}, 200, &selected)
	if len(selected.Ids) != 0 {
		t.Fatal("select-all offered an active download")
	}
	h.request(t, "POST", "/api/downloads", wire.DownloadRequest{Selection: wire.Selection{Ids: []string{"32"}}}, 202, nil)
	requireAppCopies(t, dir, 32, 0, 1)
	h.request(t, "POST", "/api/catalog/selection", wire.Selection{Ids: []string{"invalid"}}, 400, nil)
}

func TestWorkerSkipsLegacyQueuedDuplicate(t *testing.T) {
	const legacyId = "00000000-0000-7000-8000-000000000001"

	dir := t.TempDir()
	h := launch(t, dir, nil)
	defer h.close()
	h.login(t, "first")
	h.refresh(t)
	h.request(t, "POST", "/api/downloads", wire.DownloadRequest{Selection: wire.Selection{Ids: []string{"1"}}}, 202, nil)
	h.waitDone(t, 1)
	db, err := sql.Open("sqlite", filepath.Join(dir, "metadata.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("INSERT INTO packages(id,app_id,account_id,name,created_at) VALUES(?,1,'first','Legacy duplicate','2026-01-01T00:00:00Z')", legacyId); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("INSERT INTO downloads(package_id,session_id,status) VALUES(?,'local','queued')", legacyId); err != nil {
		t.Fatal(err)
	}
	h.request(t, "POST", "/api/downloads", wire.DownloadRequest{Selection: wire.Selection{Ids: []string{"2"}}}, 202, nil)
	h.waitDone(t, 2)
	var status string
	if err := db.QueryRow("SELECT status FROM downloads WHERE package_id=?", legacyId).Scan(&status); err != nil || status != "canceled" {
		t.Fatalf("legacy duplicate was executed: status=%q err=%v", status, err)
	}
	requireAppCopies(t, dir, 1, 1, 2)
}

func TestDownloadPublicationFailureRollsBackAndContinues(t *testing.T) {
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
	if _, err := db.Exec(`CREATE TRIGGER reject_publication BEFORE UPDATE OF status ON downloads
		WHEN (SELECT app_id FROM packages WHERE id=NEW.package_id)=1 AND NEW.status='done' BEGIN SELECT RAISE(ABORT,'publication failed'); END`); err != nil {
		t.Fatal(err)
	}
	h.request(t, "POST", "/api/downloads", wire.DownloadRequest{Selection: wire.Selection{Ids: []string{"1", "2"}}}, 202, nil)
	var id, status, code string
	var completed int
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := db.QueryRow("SELECT p.id,d.status,d.error FROM downloads d JOIN packages p ON p.id=d.package_id WHERE p.app_id=1").Scan(&id, &status, &code); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRow("SELECT count(*) FROM downloads d JOIN packages p ON p.id=d.package_id WHERE p.app_id=2 AND d.status='done'").Scan(&completed); err != nil {
			t.Fatal(err)
		}
		if status == "failed" && completed == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if status != "failed" || code != "storage_failed" || completed != 1 {
		t.Fatalf("publication failure not recorded: status=%q code=%q", status, code)
	}
	requireAppCopies(t, dir, 1, 0, 1)
	requireAppCopies(t, dir, 2, 1, 1)
	for _, suffix := range []string{".ipa", ".partial", ".partial.tmp"} {
		if _, err := os.Stat(filepath.Join(dir, "packages", id+suffix)); !os.IsNotExist(err) {
			t.Fatalf("failed publication left %s: %v", suffix, err)
		}
	}
}
