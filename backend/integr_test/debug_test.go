package integration_test

import (
	"context"
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

	"github.com/hyperion-cs/appleseum/applekit"

	"appleseum/backend/internal/account"
	server "appleseum/backend/internal/app"
	"appleseum/backend/internal/db"
	"appleseum/backend/internal/wire"
)

func debugHarness(t *testing.T, enabled bool) *harness {
	t.Helper()
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "packages"), 0700); err != nil {
		t.Fatal(err)
	}
	app, err := newTestServer(server.Config{DataDir: dir, Debug: enabled, DebugLogDir: filepath.Join(dir, "logs"), Apple: &testApple{signIn: func(ctx context.Context, email, password, code string) (account.Session, error) {
		// The shared client rejects HTTP before dialing; no Apple requests in tests.
		req, _ := http.NewRequestWithContext(ctx, "POST", "http://gsa.apple.com/grandslam/GsService2?secret=query-marker", strings.NewReader(password))
		req.Header.Set("Cookie", "cookie-marker")
		applekit.NewHttpClient(nil).Do(req)
		return nil, &applekit.LoginError{Stage: "authenticate", Reason: "missing_session", Status: 200, Message: "bad_login_configurator"}
	}}})
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(app.Handler())
	t.Cleanup(func() { httpServer.Close(); app.Close() })
	h := &harness{app: app, http: httpServer, client: httpServer.Client()}
	var state wire.State
	h.request(t, "GET", "/api/state", nil, 200, &state)
	h.csrf = state.Csrf
	if state.Mode.Debug != enabled {
		t.Fatal("debug capability mismatch")
	}
	return h
}

func TestDebugDisabled(t *testing.T) {
	h := debugHarness(t, false)
	h.request(t, "GET", "/api/debug", nil, 404, nil)
	h.request(t, "GET", "/api/debug/selection", nil, 404, nil)
	h.request(t, "POST", "/api/debug/responses", wire.DebugExportRequest{Ids: []int64{1, 2}}, 404, nil)
	h.request(t, "DELETE", "/api/debug", nil, 404, nil)
}

func TestDebugSharingUsesHttpEvents(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "packages"), 0700); err != nil {
		t.Fatal(err)
	}
	cfg := server.Config{DataDir: dir, Debug: true, DebugLogDir: filepath.Join(dir, "logs"),
		Apple: &testApple{signIn: func(context.Context, string, string, string) (account.Session, error) {
			return &fakeApple{owner: "share-log"}, nil
		}, open: func(db.Identity) (account.Session, error) { return &fakeApple{owner: "share-log"}, nil }},
	}

	open := func() *harness {
		app, err := newTestServer(cfg)
		if err != nil {
			t.Fatal(err)
		}
		h := &harness{app: app, http: httptest.NewServer(app.Handler()), dist: httptest.NewServer(app.Distribution()), client: &http.Client{}}
		var state wire.State
		h.request(t, "GET", "/api/state", nil, 200, &state)
		h.csrf = state.Csrf
		return h
	}

	h := open()
	defer func() { h.close() }()
	h.request(t, "POST", "/api/login", wire.LoginRequest{Email: "synthetic@example.test", Password: "synthetic"}, 200, nil)
	h.refresh(t)
	h.request(t, "POST", "/api/downloads", wire.DownloadRequest{Selection: wire.Selection{Ids: []string{"1", "2"}}}, 202, nil)
	h.waitDone(t, 2)
	var library wire.LibraryPage
	h.request(t, "GET", "/api/library", nil, 200, &library)
	selection := wire.Selection{Ids: []string{library.Items[0].Id, library.Items[1].Id}}
	var share wire.Share
	h.request(t, "POST", "/api/shares", selection, 201, &share)
	h.request(t, "POST", "/api/shares", wire.Selection{}, 400, nil)
	h.request(t, "POST", "/api/shares/stop", wire.Selection{Ids: []string{"00000000-0000-4000-8000-000000000001"}}, 204, nil)
	for _, path := range []string{"/manifests/" + share.Id + ".plist", "/packages/" + share.Id + "/" + selection.Ids[0] + ".ipa"} {
		base := h.dist.URL
		if strings.HasPrefix(path, "/packages/") {
			base = h.http.URL
		}
		response, err := h.client.Get(base + path)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, response.Body)
		response.Body.Close()
		if response.StatusCode != 200 {
			t.Fatal("distribution request failed", response.StatusCode)
		}
	}
	var page wire.DebugPage
	h.request(t, "GET", "/api/debug?source=API&q=/packages/", nil, 200, &page)
	if page.Total != 0 {
		t.Fatal("IPA request duplicated in API events", page.Items)
	}
	h.request(t, "GET", "/api/debug?source=Hosting", nil, 200, &page)
	if page.Total != 5 {
		t.Fatal("hosting filter missed share requests", page.Items)
	}
	for _, event := range page.Items {
		if !event.HasResponse || (event.Source != "Hosting" && !(event.Source == "API" && strings.HasPrefix(event.Target, "/api/shares"))) {
			t.Fatal("share filter lost HTTP exchange", event)
		}
	}
	h.request(t, "POST", "/api/shares/stop", wire.Selection{Ids: []string{share.Id}}, 204, nil)
	// Restart retains the HTTP journal without adding lifecycle events.
	h.close()
	h = open()
	h.request(t, "POST", "/api/shares/stop", wire.Selection{Ids: []string{share.Id}}, 204, nil)
	h.request(t, "GET", "/api/debug?limit=100", nil, 200, &page)
	starts, stops := 0, 0
	for _, event := range page.Items {
		if event.Source == "Auth" || event.Target == "sharing_started" || event.Target == "sharing_stopped" {
			t.Fatal("redundant lifecycle event", event)
		}
		if event.Source == "API" && event.Target == "/api/shares" && event.Status == 201 {
			starts++
		}
		if event.Source == "API" && event.Target == "/api/shares/stop" && event.Status == 204 {
			stops++
		}
	}
	if starts != 1 || stops != 3 {
		t.Fatal("successful share HTTP requests missing", starts, stops)
	}
	h.request(t, "GET", "/api/debug?source=Hosting&limit=100", nil, 200, &page)
	if page.Total != 7 {
		t.Fatal("share filter lost events after restart", page.Items)
	}
	for _, event := range page.Items {
		if event.Source != "Hosting" && !strings.HasPrefix(event.Target, "/api/shares") {
			t.Fatal("unrelated request in hosting filter", event)
		}
	}
}

func TestDebugJournalPrivacyAndAccess(t *testing.T) {
	h := debugHarness(t, true)
	response, err := h.client.Get(h.http.URL + "/api/debug")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 403 {
		t.Fatal("debug read without CSRF token")
	}
	h.request(t, "POST", "/api/login", wire.LoginRequest{Email: "email-marker@example.invalid", Password: "password-marker", Code: "123456"}, 502, nil)
	h.request(t, "GET", "/private-path-marker?query=query-marker", nil, 404, nil)
	distribution := httptest.NewServer(h.app.Distribution())
	defer distribution.Close()
	response, err = h.client.Get(distribution.URL + "/packages/private-path-marker/another-private-marker.ipa?secret=query-marker")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	var info wire.DebugInfo
	h.request(t, "GET", "/api/debug/info", nil, 200, &info)
	if info.Capacity != 1_000 || info.Runtime == "" || info.StartedAt == "" || !filepath.IsAbs(info.Executable) || info.Arguments == nil {
		t.Fatal("missing runtime metadata")
	}
	var snapshot wire.DebugPage
	h.request(t, "GET", "/api/debug", nil, 200, &snapshot)
	found := map[string]bool{}
	for _, event := range snapshot.Items {
		found[event.Source] = true
		if event.Source == "Apple" && (event.Target != "gsa.apple.com /grandslam/GsService2" || event.Detail != "transport") {
			t.Fatal("Apple request context not observed")
		}
		if event.Source == "API" && event.Target == "/api/login" && event.Method != "POST" {
			t.Fatal("API method lost")
		}
		if event.Source == "Hosting" && event.Target != "/packages/{share}/{file}.ipa" {
			t.Fatal("distribution values leaked")
		}
		if event.Source == "API" && event.Target == "/api/login" && !strings.Contains(event.Detail, "bad_login_configurator") {
			t.Fatal("safe sign-in details missing")
		}
	}
	if !found["Apple"] || found["Auth"] || !found["API"] || !found["Hosting"] {
		t.Fatal("request sources missing")
	}
	encoded, _ := json.Marshal(snapshot)
	for _, secret := range []string{h.csrf, "email-marker", "password-marker", "123456", "query-marker", "private-path-marker", "cookie-marker"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatal("private data in journal")
		}
	}
	h.request(t, "DELETE", "/api/debug", nil, 204, nil)
	h.request(t, "GET", "/api/debug", nil, 200, &snapshot)
	if len(snapshot.Items) != 0 {
		t.Fatal("clear or poll exclusion failed")
	}
}

func TestDebugJournalBoundAndConcurrentReads(t *testing.T) {
	h := debugHarness(t, true)
	var wg sync.WaitGroup
	for worker := 0; worker < 4; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 130; i++ {
				response, err := h.client.Get(h.http.URL + "/api/library")
				if err != nil {
					t.Error(err)
					return
				}
				io.Copy(io.Discard, response.Body)
				response.Body.Close()
			}
		}()
	}
	for i := 0; i < 5; i++ {
		var concurrent wire.DebugPage
		h.request(t, "GET", "/api/debug", nil, 200, &concurrent)
	}
	wg.Wait()
	// The journal stays bounded even while several clients generate events.
	var snapshot wire.DebugPage
	h.request(t, "GET", "/api/debug", nil, 200, &snapshot)
	if snapshot.Total != 520 || len(snapshot.Items) != 10 {
		t.Fatalf("journal total %d, page length %d", snapshot.Total, len(snapshot.Items))
	}
	for i := 1; i < len(snapshot.Items); i++ {
		if snapshot.Items[i-1].Id <= snapshot.Items[i].Id {
			t.Fatal("journal order changed")
		}
	}
}

func TestDebugDoesNotCaptureBrowserEncryptionKey(t *testing.T) {
	h := debugHarness(t, true)
	req, _ := http.NewRequest("GET", h.http.URL+"/api/profile", nil)
	req.Header.Set("X-Session-Key", "synthetic-browser-key-marker")
	response, err := h.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	var page wire.DebugPage
	h.request(t, "GET", "/api/debug", nil, 200, &page)
	if len(page.Items) == 0 {
		t.Fatal("request not captured")
	}
	for _, suffix := range []string{"", "?raw=1"} {
		raw := h.request(t, "GET", fmt.Sprintf("/api/debug/responses/%d%s", page.Items[0].Id, suffix), nil, 200, nil)
		if strings.Contains(string(raw), "synthetic-browser-key-marker") || strings.Contains(strings.ToLower(string(raw)), "x-session-key") {
			t.Fatal("browser encryption key leaked to HTTP capture")
		}
	}
}

func TestDebugSelectionRoute(t *testing.T) {
	h := debugHarness(t, true)
	for range 12 {
		h.request(t, "GET", "/api/library?limit=1", nil, 200, nil)
	}
	var result wire.DebugSelectionResult
	h.request(t, "GET", "/api/debug/selection?source=API&q=/api/library", nil, 200, &result)
	if len(result.Ids) != 12 {
		t.Fatal("selection missed responses outside the page", result.Ids)
	}
	var page wire.DebugPage
	h.request(t, "GET", "/api/debug?source=API&q=/api/library&limit=2", nil, 200, &page)
	if len(page.Items) != 2 || result.Ids[0] != page.Items[0].Id {
		t.Fatal("page and selection filters differ")
	}
}
