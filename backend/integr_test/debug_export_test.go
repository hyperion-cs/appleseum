package integration_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	server "appleseum/backend/internal/app"
	"appleseum/backend/internal/wire"
)

func TestDebugExportSelectedExchanges(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "packages"), 0700); err != nil {
		t.Fatal(err)
	}
	app, err := newTestServer(server.Config{
		DataDir: dir, Debug: true, DebugLogDir: filepath.Join(dir, "logs"),
		Web: fstest.MapFS{"login": {Data: []byte("first-response-marker")}, "available": {Data: []byte("second-response-marker")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()

	request := func(method, path, body, token string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, "http://localhost"+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-CSRF-Token", token)
		req.Header.Set("X-Test-Request", "request-header-marker")
		r := httptest.NewRecorder()
		app.Handler().ServeHTTP(r, req)
		return r
	}

	var state wire.State
	if err := json.Unmarshal(request("GET", "/api/state", "", "").Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	request("GET", "/login", "", state.Csrf)
	request("GET", "/available", "", state.Csrf)
	var page wire.DebugPage
	if err := json.Unmarshal(request("GET", "/api/debug?limit=5", "", state.Csrf).Body.Bytes(), &page); err != nil || len(page.Items) != 2 {
		t.Fatal("exchange fixtures missing", err)
	}
	ids := []int64{page.Items[0].Id, page.Items[1].Id}
	input, _ := json.Marshal(wire.DebugExportRequest{Ids: ids})
	r := request("POST", "/api/debug/responses", string(input), state.Csrf)
	if r.Code != 200 || r.Header().Get("Content-Type") != "application/json; charset=utf-8" || r.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("export status %d", r.Code)
	}
	var archive struct {
		Log struct {
			Version string
			Entries []json.RawMessage
		}
	}
	if err := json.Unmarshal(r.Body.Bytes(), &archive); err != nil || archive.Log.Version != "1.2" || len(archive.Log.Entries) != len(ids) {
		t.Fatal("invalid HAR", err)
	}
	if r.Header().Get("Content-Disposition") != `attachment; filename="exchanges.har"` {
		t.Fatal("incorrect batch filename")
	}
	for _, entry := range archive.Log.Entries {
		var metadata struct {
			EventId int64 `json:"_eventId"`
		}
		if err := json.Unmarshal(entry, &metadata); err != nil {
			t.Fatal(err)
		}
		single := request("GET", fmt.Sprintf("/api/debug/responses/%d", metadata.EventId), "", state.Csrf)
		var one struct {
			Log struct{ Entries []json.RawMessage }
		}
		if err := json.Unmarshal(single.Body.Bytes(), &one); err != nil {
			t.Fatal(err)
		}
		if single.Code != 200 || len(one.Log.Entries) != 1 || !bytes.Equal(entry, one.Log.Entries[0]) || !bytes.Contains(entry, []byte("request-header-marker")) || !bytes.Contains(entry, []byte("response-marker")) {
			t.Fatal("HAR changed or omitted a selected exchange")
		}
		if single.Header().Get("Content-Disposition") != fmt.Sprintf(`attachment; filename="exchange-%d.har"`, metadata.EventId) {
			t.Fatal("incorrect single filename")
		}
	}

	for _, tc := range []struct {
		body, token string
		status      int
	}{
		{string(input), "", 403},
		{`{"ids":[]}`, state.Csrf, 400},
		{`{"ids":[-1]}`, state.Csrf, 400},
		{fmt.Sprintf(`{"ids":[%d,%d]}`, ids[0], ids[0]), state.Csrf, 400},
		{fmt.Sprintf(`{"ids":[%d,999999]}`, ids[0]), state.Csrf, 404},
	} {
		r := request("POST", "/api/debug/responses", tc.body, tc.token)
		if r.Code != tc.status || strings.Contains(r.Header().Get("Content-Disposition"), ".har") {
			t.Fatalf("invalid selection or access: status %d, want %d", r.Code, tc.status)
		}
	}
	oversized, _ := json.Marshal(wire.DebugExportRequest{Ids: make([]int64, wire.MaxSelectionSize+1)})
	if r := request("POST", "/api/debug/responses", string(oversized), state.Csrf); r.Code != 400 {
		t.Fatal("unbounded selection accepted")
	}
	if err := json.Unmarshal(request("GET", "/api/debug?limit=5", "", state.Csrf).Body.Bytes(), &page); err != nil || page.Total != 2 {
		t.Fatal("debug export logged itself", err)
	}
	request("DELETE", "/api/debug", "", state.Csrf)
	if r := request("POST", "/api/debug/responses", string(input), state.Csrf); r.Code != 404 {
		t.Fatal("removed exchanges still downloadable")
	}
}
