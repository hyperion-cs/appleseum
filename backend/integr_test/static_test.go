package integration_test

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"testing/fstest"

	server "appleseum/backend/internal/app"
)

func TestPublicRootCanLoadWorkspaceWithoutAppleAccount(t *testing.T) {
	const (
		path   = "/assets/Workspace-test.js"
		script = "export default {}"
	)
	dir := t.TempDir()
	if err := os.Mkdir(dir+"/packages", 0o700); err != nil {
		t.Fatal(err)
	}
	app, err := newTestServer(server.Config{
		DataDir: dir,
		Public:  true,
		Web:     fstest.MapFS{"assets/Workspace-test.js": {Data: []byte(script)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewTLSServer(app.Handler())
	t.Cleanup(func() { ts.Close(); app.Close() })
	root, guest := newBrowser(t, ts), newBrowser(t, ts)
	guest.request("GET", path, nil, 200, nil)
	root.login("root@appleseum", app.ServicePassword())
	if state := root.state(); !state.Mode.Service || state.Account != nil {
		t.Fatal("expected root without an Apple account")
	}
	if body := root.request("GET", path, nil, 200, nil); string(body) != script {
		t.Fatal("workspace script was not served")
	}
	guest.request("GET", "/api/library", nil, 401, nil)
}

func TestCompressedIndexAndStaticChunks(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(dir+"/packages", 0o700); err != nil {
		t.Fatal(err)
	}
	html := []byte("<!doctype html><title>Test</title>")
	var compressed bytes.Buffer
	gz := gzip.NewWriter(&compressed)
	gz.Write(html)
	gz.Close()
	assets := fstest.MapFS{"index.html": {Data: html}, "index.html.gz": {Data: compressed.Bytes()}, "assets/Workspace-test.js": {Data: []byte("export default {}")}}
	app, err := newTestServer(server.Config{DataDir: dir, Web: assets})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	httpServer := httptest.NewServer(app.Handler())
	defer httpServer.Close()
	for _, encoding := range []string{"gzip", "gzip;q=0"} {
		req, _ := http.NewRequest("GET", httpServer.URL+"/", nil)
		req.Header.Set("Accept-Encoding", encoding)
		resp, err := httpServer.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("index returned %d", resp.StatusCode)
		}
		if encoding == "gzip" {
			if resp.Header.Get("Content-Encoding") != "gzip" {
				t.Fatal("compression not served")
			}
			reader, err := gzip.NewReader(bytes.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			body, _ = io.ReadAll(reader)
			reader.Close()
		} else if resp.Header.Get("Content-Encoding") != "" {
			t.Fatal("q=0 encoding was used")
		}
		if !bytes.Equal(body, html) {
			t.Fatal("index body corrupted")
		}
	}
	resp, err := httpServer.Client().Get(httpServer.URL + "/assets/Workspace-test.js")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatal("static chunk unavailable without account")
	}
}

func TestCompressedRouteAndEncodingPreferences(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(dir+"/packages", 0o700); err != nil {
		t.Fatal(err)
	}
	assets := fstest.MapFS{
		"settings":    {Data: []byte("<!doctype html><title>Settings</title>")},
		"settings.gz": {Data: []byte("gzip fixture")},
		"settings.br": {Data: []byte("brotli fixture")},
	}
	app, err := newTestServer(server.Config{DataDir: dir, Web: assets})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	for _, tt := range []struct{ header, encoding, body string }{
		{"br, gzip", "br", "brotli fixture"},
		{"br;q=0.2, gzip;q=0.9", "gzip", "gzip fixture"},
		{"*;q=1, br;q=0", "gzip", "gzip fixture"},
		{"*;q=1, br;q=0, gzip;q=0", "", string(assets["settings"].Data)},
		{"br;q=NaN, gzip", "gzip", "gzip fixture"},
		{"BR;Q=1", "br", "brotli fixture"},
	} {
		t.Run(tt.header, func(t *testing.T) {
			r := httptest.NewRequest("GET", "http://localhost/settings", nil)
			r.Header.Set("Accept-Encoding", tt.header)
			w := httptest.NewRecorder()
			app.Handler().ServeHTTP(w, r)
			if w.Code != 200 || w.Header().Get("Content-Type") != "text/html; charset=utf-8" || w.Header().Get("Content-Encoding") != tt.encoding || w.Body.String() != tt.body {
				t.Fatalf("status=%d type=%s encoding=%s body=%q", w.Code, w.Header().Get("Content-Type"), w.Header().Get("Content-Encoding"), w.Body.String())
			}
		})
	}
}
