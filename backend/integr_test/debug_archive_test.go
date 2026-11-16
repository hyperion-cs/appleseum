package integration_test

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"io"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/hyperion-cs/appleseum/applekit"

	"appleseum/backend/internal/account"
	server "appleseum/backend/internal/app"
	"appleseum/backend/internal/wire"
)

func TestDebugArchiveRotationPagingAndRestart(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "packages"), 0700); err != nil {
		t.Fatal(err)
	}
	logs := filepath.Join(dir, "logs")
	cfg := server.Config{DataDir: dir, Debug: true, DebugLogDir: logs, Web: fstest.MapFS{"debug": {Data: []byte("raw-token-exchange-marker")}, "login": {Data: []byte("raw-token-exchange-marker")}}, Apple: &testApple{signIn: func(context.Context, string, string, string) (account.Session, error) {
		return nil, &applekit.LoginError{Stage: "authenticate", Reason: "upstream_http", Status: 403, BodyFormat: "empty", BodyBytes: 0}
	}}}
	app, err := newTestServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if app != nil {
			app.Close()
		}
	}()

	request := func(method, path, body string, csrf string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, "http://localhost"+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-CSRF-Token", csrf)
		rec := httptest.NewRecorder()
		app.Handler().ServeHTTP(rec, req)
		return rec
	}

	var state wire.State
	json.Unmarshal(request("GET", "/api/state", "", "").Body.Bytes(), &state)
	token := state.Csrf
	if response := request("GET", "/debug", "", token); response.Body.String() != "raw-token-exchange-marker" {
		t.Fatal("fixture not served")
	}
	var page wire.DebugPage

	read := func(path string) {
		t.Helper()
		r := request("GET", path, "", token)
		if r.Code != 200 {
			t.Fatalf("debug status %d", r.Code)
		}
		if err := json.Unmarshal(r.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
	}

	read("/api/debug?limit=5")
	if page.Total != 0 {
		t.Fatal("debug page logged itself")
	}
	request("GET", "/login", "", token)
	read("/api/debug?limit=5")
	if len(page.Items) != 1 || page.Items[0].Target != "/login" || !page.Items[0].HasResponse {
		t.Fatal("real page path or response missing")
	}
	id := strconv.FormatInt(page.Items[0].Id, 10)
	if r := request("GET", "/api/debug/responses/"+id, "", token); !strings.Contains(r.Body.String(), "raw-token-exchange-marker") {
		t.Fatal("response bytes were changed")
	}
	if r := request("GET", "/api/debug/responses/"+id, "", ""); r.Code != 403 {
		t.Fatal("response download without CSRF")
	}
	r := request("POST", "/api/login", `{"email":"synthetic@example.invalid","password":"private-marker","code":""}`, token)
	if r.Code != 502 || !strings.Contains(r.Body.String(), "apple_sign_in_denied") {
		t.Fatal("upstream 403 reported as credential failure")
	}
	firstFiles, _ := filepath.Glob(filepath.Join(logs, "events-*.csv"))
	if len(firstFiles) != 1 {
		t.Fatal("initial segment missing")
	}
	before, _ := os.Stat(firstFiles[0])
	for i := 0; i < 24; i++ {
		request("GET", "/api/library", "", token)
	}
	after, _ := os.Stat(firstFiles[0])
	if !os.SameFile(before, after) || after.Size() <= before.Size() {
		t.Fatal("CSV replaced instead of appended")
	}
	read("/api/debug?limit=5")
	first := page.Items[0].Id
	next := page.Items[len(page.Items)-1].Cursor
	read("/api/debug?limit=5&cursor=" + url.QueryEscape(next))
	if page.Items[0].Id >= first || !page.Previous {
		t.Fatal("next cursor failed")
	}
	previous := page.Items[0].Cursor
	read("/api/debug?limit=5&cursor=" + url.QueryEscape(previous) + "&move=prev")
	if page.Items[0].Id != first {
		t.Fatal("previous cursor failed")
	}
	read("/api/debug?source=API&q=upstream_http&limit=5")
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].Source != "API" {
		t.Fatal("server-side search/filter failed")
	}
	if r := request("GET", "/api/debug?sort=status", "", token); r.Code != 400 {
		t.Fatal("unexpected selectable ordering")
	}
	// Page and API failure, then 24 library reads; no duplicate Auth event.
	read("/api/debug?limit=5")
	initial := page.Total
	for i := initial; i < 1_001; i++ {
		request("GET", "/api/library", "", token)
	}
	files, _ := filepath.Glob(filepath.Join(logs, "events-*.csv"))
	bodies, _ := filepath.Glob(filepath.Join(logs, "exchange-*.http"))
	if len(files) != 2 || len(bodies) > 1_000 {
		t.Fatal("archive rotation not bounded")
	}
	if _, err := os.Stat(firstFiles[0]); !os.IsNotExist(err) {
		t.Fatal("oldest segment not deleted")
	}
	csvResponse := request("GET", "/api/debug/csv", "", token)
	records, err := csv.NewReader(strings.NewReader(csvResponse.Body.String())).ReadAll()
	if err != nil || len(records) != 502 {
		t.Fatalf("CSV rotation rows=%d err=%v", len(records), err)
	}
	for _, file := range append(files, bodies...) {
		info, err := os.Stat(file)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatal("non-private log file")
		}
	}
	read("/api/debug?limit=5")
	if page.Total != 501 {
		t.Fatal("UI journal not bounded")
	}
	latest := page.Items[0].Id
	if r := request("GET", "/api/debug/responses/"+id, "", token); r.Code != 404 {
		t.Fatal("evicted body still exposed")
	}
	if err := app.Close(); err != nil {
		t.Fatal(err)
	}
	app = nil
	app, err = newTestServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	json.Unmarshal(request("GET", "/api/state", "", "").Body.Bytes(), &state)
	token = state.Csrf
	read("/api/debug?limit=5")
	if page.Total != 501 || page.Items[0].Id != latest {
		t.Fatal("journal not restored")
	}
	if r := request("DELETE", "/api/debug", "", token); r.Code != 204 {
		t.Fatal("clear failed")
	}
	read("/api/debug?limit=5")
	if page.Total != 0 || len(page.Items) != 0 {
		t.Fatal("clear did not empty journal")
	}
	bodies, _ = filepath.Glob(filepath.Join(logs, "exchange-*.http"))
	if len(bodies) != 0 {
		t.Fatal("clear left response files")
	}
	// A request after clearing starts a fresh appendable journal.
	response := request("GET", "/api/library", "", token)
	io.Copy(io.Discard, response.Result().Body)
	read("/api/debug?limit=5")
	if page.Total != 1 {
		t.Fatal("journal did not resume after clearing")
	}
}
