package app

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"appleseum/backend/internal/db"
	"appleseum/backend/internal/wire"
)

const (
	lifecyclePackage = "00000000-0000-4000-8000-000000000001"
	lifecyclePayload = "saved IPA fixture"
)

func dataDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "packages"), 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func newApp(t *testing.T, dir string, public bool) *App {
	t.Helper()
	a, err := New(Config{DataDir: dir, Public: public})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	return a
}

func database(t *testing.T, dir string) *sql.DB {
	t.Helper()
	conn, err := sql.Open("sqlite", filepath.Join(dir, "metadata.db")+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

// csrf reads the local workspace's token the way the interface does.
func csrf(t *testing.T, a *App) string {
	t.Helper()
	out := httptest.NewRecorder()
	a.Handler().ServeHTTP(out, httptest.NewRequest("GET", "http://localhost/api/state", nil))
	var state wire.State
	if err := json.Unmarshal(out.Body.Bytes(), &state); err != nil || state.Csrf == "" {
		t.Fatal("state unavailable", out.Code, err)
	}
	return state.Csrf
}

// pausedBody holds a request inside its handler until released.
type pausedBody struct {
	io.Reader
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func newPausedBody(body string) *pausedBody {
	return &pausedBody{Reader: strings.NewReader(body), entered: make(chan struct{}), release: make(chan struct{})}
}

func (b *pausedBody) Read(p []byte) (int, error) {
	b.once.Do(func() { close(b.entered); <-b.release })
	return b.Reader.Read(p)
}

func (*pausedBody) Close() error { return nil }

func paused(t *testing.T, a *App, method, path string, body *pausedBody) (*httptest.ResponseRecorder, chan struct{}) {
	t.Helper()
	req := httptest.NewRequest(method, "http://localhost"+path, body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrf(t, a))
	out := httptest.NewRecorder()
	served := make(chan struct{})
	go func() { defer close(served); a.Handler().ServeHTTP(out, req) }()
	<-body.entered
	return out, served
}

func TestShutdownDrainsAcceptedRequestBeforeClosingStorage(t *testing.T) {
	a := newApp(t, dataDir(t), false)
	body := newPausedBody(`{"language":"ru","theme":"light"}`)
	out, served := paused(t, a, "PUT", "/api/preferences", body)
	closed := make(chan error, 1)
	go func() { closed <- a.Close() }()
	select {
	case err := <-closed:
		close(body.release)
		<-served
		t.Fatalf("closed storage during an accepted request: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	late := httptest.NewRecorder()
	a.Handler().ServeHTTP(late, httptest.NewRequest("GET", "http://localhost/api/state", nil))
	if late.Code != http.StatusServiceUnavailable {
		t.Errorf("shutdown accepted a new request: %d", late.Code)
	}
	close(body.release)
	select {
	case <-served:
	case <-time.After(3 * time.Second):
		t.Fatal("accepted request did not finish")
	}
	if out.Code != http.StatusOK {
		t.Fatalf("accepted write lost its storage: %d %s", out.Code, out.Body.String())
	}
	if err := <-closed; err != nil {
		t.Fatal(err)
	}
}

func seedShares(t *testing.T, conn *sql.DB, dir string) []string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "packages", lifecyclePackage+".ipa"), []byte(lifecyclePayload), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		"INSERT INTO accounts(id) VALUES('fixture')",
		fmt.Sprintf("INSERT INTO packages(id,account_id,app_id,name,created_at,bundle,version,display_version,minimum_os,size) VALUES('%s','fixture',1,'Fixture','2026-11-09','test.fixture','1','1.0','15.0',%d)", lifecyclePackage, len(lifecyclePayload)),
		"INSERT INTO sessions(id,scope,csrf) VALUES('first','public','fixture'),('second','public','fixture')",
	} {
		if _, err := conn.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	ids := []string{db.NewId(), db.NewId()}
	for i, session := range []string{"first", "second"} {
		if _, err := conn.Exec("INSERT INTO shares(id,created_at,creator_session_id) VALUES(?,'2026-11-09',?)", ids[i], session); err != nil {
			t.Fatal(err)
		}
		if _, err := conn.Exec("INSERT INTO share_packages(share_id,package_id) VALUES(?,?)", ids[i], lifecyclePackage); err != nil {
			t.Fatal(err)
		}
	}
	return ids
}

func checkStorage(t *testing.T, conn *sql.DB, dir string, shares int) {
	t.Helper()
	for table, want := range map[string]int{"shares": shares, "share_packages": shares, "packages": 1, "accounts": 1} {
		var got int
		if err := conn.QueryRow("SELECT count(*) FROM " + table).Scan(&got); err != nil || got != want {
			t.Fatalf("%s: got %d, want %d; error=%v", table, got, want, err)
		}
	}
	if body, err := os.ReadFile(filepath.Join(dir, "packages", lifecyclePackage+".ipa")); err != nil || string(body) != lifecyclePayload {
		t.Fatalf("share cleanup damaged the IPA: %q, %v", body, err)
	}
}

func TestCloseStopsAllShares(t *testing.T) {
	for _, public := range []bool{false, true} {
		t.Run(fmt.Sprintf("public=%t", public), func(t *testing.T) {
			dir := dataDir(t)
			a := newApp(t, dir, public)
			conn := database(t, dir)
			seedShares(t, conn, dir)
			if err := a.Close(); err != nil {
				t.Fatal(err)
			}
			// Read directly: starting a server here would hide a missing shutdown cleanup.
			checkStorage(t, conn, dir, 0)
		})
	}
}

func TestStartupDiscardsSharesLeftByInterruptedShutdown(t *testing.T) {
	for _, public := range []bool{false, true} {
		t.Run(fmt.Sprintf("public=%t", public), func(t *testing.T) {
			dir := dataDir(t)
			s, err := db.Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			conn := database(t, dir)
			ids := seedShares(t, conn, dir)
			checkStorage(t, conn, dir, 2)
			// The state a crashed process leaves: no Close of the server.
			if err = s.Close(); err != nil {
				t.Fatal(err)
			}
			a := newApp(t, dir, public)
			checkStorage(t, conn, dir, 0)
			for _, id := range ids {
				for _, path := range []string{"/manifests/" + id + ".plist", "/packages/" + id + "/" + lifecyclePackage + ".ipa"} {
					out := httptest.NewRecorder()
					a.Distribution().ServeHTTP(out, httptest.NewRequest("GET", "http://localhost"+path, nil))
					if out.Code != http.StatusNotFound {
						t.Fatalf("stale share accessible: %s returned %d", path, out.Code)
					}
				}
			}
		})
	}
}

func TestShutdownRemovesSharesCreatedByAcceptedRequest(t *testing.T) {
	dir := dataDir(t)
	a := newApp(t, dir, false)
	conn := database(t, dir)
	seedShares(t, conn, dir)
	body := newPausedBody(`{"ids":["` + lifecyclePackage + `"]}`)
	out, served := paused(t, a, "POST", "/api/shares", body)
	// Let the accepted request create its share while Close waits for it.
	if _, err := conn.Exec("DELETE FROM shares"); err != nil {
		close(body.release)
		<-served
		t.Fatal(err)
	}
	a.BeginShutdown()
	closed := make(chan error, 1)
	go func() { closed <- a.Close() }()
	close(body.release)
	<-served
	if err := <-closed; err != nil {
		t.Fatal(err)
	}
	if out.Code != http.StatusCreated {
		t.Fatalf("accepted share request failed: %d %s", out.Code, out.Body.String())
	}
	checkStorage(t, conn, dir, 0)
}
