package integration_test

import (
	"archive/zip"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"appleseum/backend/internal/account"
	server "appleseum/backend/internal/app"
	"appleseum/backend/internal/wire"
	"github.com/hyperion-cs/appleseum/applekit"
	"howett.net/plist"
)

type updatingApple struct {
	*fakeApple
	mu          sync.Mutex
	latest      string
	unavailable bool
	checks      int
}

func (f *updatingApple) Versions(context.Context, int64) ([]string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.checks++
	if f.unavailable {
		return nil, "", errors.New("version service offline")
	}
	return []string{"101", "102"}, f.latest, nil
}
func (f *updatingApple) VersionInfo(_ context.Context, _ int64, id string, _ func(float64)) (applekit.VersionMetadata, error) {
	return applekit.VersionMetadata{Version: "1.0", Build: id, MinimumOs: "16.0"}, nil
}
func (f *updatingApple) PrepareDownload(_ context.Context, _ int64, id string) (applekit.Download, error) {
	f.mu.Lock()
	if id == "" {
		id = f.latest
	}
	f.mu.Unlock()
	return func(_ context.Context, path string, progress func(applekit.DownloadProgress), _ applekit.DownloadOptions) (string, error) {
		file, err := os.Create(path)
		if err != nil {
			return "", err
		}
		defer file.Close()
		archive := zip.NewWriter(file)
		writer, err := archive.Create("Payload/App.app/Info.plist")
		if err != nil {
			return "", err
		}
		data, err := plist.Marshal(map[string]string{"CFBundleIdentifier": "test.update", "CFBundleVersion": id, "CFBundleShortVersionString": "1.0", "MinimumOSVersion": "16.0"}, plist.XMLFormat)
		if err != nil {
			return "", err
		}
		if _, err = writer.Write(data); err != nil {
			return "", err
		}
		if err = archive.Close(); err != nil {
			return "", err
		}
		progress(applekit.DownloadProgress{Received: int64(len(data)), Total: int64(len(data))})
		return path, nil
	}, nil
}

func TestRefreshOffersNewBuildAndPreservesSavedIPA(t *testing.T) {
	fake := &updatingApple{fakeApple: &fakeApple{owner: "updates"}, latest: "101"}
	dir := t.TempDir()
	if err := os.MkdirAll(dir+"/packages", 0700); err != nil {
		t.Fatal(err)
	}
	app, err := newTestServer(server.Config{DataDir: dir, Apple: &testApple{signIn: func(context.Context, string, string, string) (account.Session, error) { return fake, nil }}})
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{app: app, http: httptest.NewServer(app.Handler()), dist: httptest.NewServer(app.Distribution()), client: &http.Client{Timeout: 5 * time.Second}}
	defer h.close()
	var state wire.State
	h.request(t, "GET", "/api/state", nil, 200, &state)
	h.csrf = state.Csrf
	h.request(t, "POST", "/api/login", wire.LoginRequest{Email: "updates@example.test", Password: "synthetic"}, 200, &state)
	h.refresh(t)
	request := wire.DownloadRequest{Selection: wire.Selection{Ids: []string{"1"}}}
	h.request(t, "POST", "/api/downloads", request, 202, nil)
	h.waitDone(t, 1)
	check := func(update bool) {
		t.Helper()
		var page wire.CatalogPage
		h.request(t, "GET", "/api/catalog?q=App+01", nil, 200, &page)
		for _, item := range page.Items {
			if item.Id == "1" {
				if !item.InLibrary || item.UpdateAvailable != update {
					t.Fatalf("catalog: %+v", item)
				}
				return
			}
		}
		t.Fatal("missing app")
	}
	h.refresh(t)
	check(false)
	fake.mu.Lock()
	fake.latest = "102"
	fake.mu.Unlock()
	h.refresh(t)
	check(true) // Refresh must bypass the version-list lifetime.
	var selection wire.SelectionResult
	h.request(t, "POST", "/api/catalog/selection", request.Selection, 200, &selection)
	if len(selection.Ids) != 1 {
		t.Fatal("update excluded from selection", selection)
	}
	h.request(t, "POST", "/api/downloads", request, 202, nil)
	h.waitDone(t, 2)
	check(false)
	requireAppCopies(t, dir, 1, 2, 2)
	h.refresh(t)
	check(false)
	h.request(t, "POST", "/api/downloads", request, 202, nil)
	requireAppCopies(t, dir, 1, 2, 2)
	fake.mu.Lock()
	fake.unavailable = true
	checks := fake.checks
	fake.mu.Unlock()
	h.refresh(t)
	check(false)
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.checks <= checks {
		t.Fatal("refresh did not recheck versions")
	}
}
