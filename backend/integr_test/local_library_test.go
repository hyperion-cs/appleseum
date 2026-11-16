package integration_test

import (
	"bytes"
	"context"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"appleseum/backend/internal/account"
	server "appleseum/backend/internal/app"
	"appleseum/backend/internal/db"
	"appleseum/backend/internal/wire"
)

func TestLocalLibraryWithoutAppleSession(t *testing.T) {
	dir := t.TempDir()
	h := launch(t, dir, nil)
	defer func() { h.close() }()
	var library wire.LibraryPage
	h.request(t, "GET", "/api/library", nil, 200, &library)
	if len(library.Items) != 0 || library.Total != 0 {
		t.Fatal("new local library is not empty")
	}
	h.login(t, "ipa-artwork")
	h.refresh(t)
	h.request(t, "POST", "/api/downloads", wire.DownloadRequest{Selection: wire.Selection{Ids: []string{"1"}}}, 202, nil)
	h.waitDone(t, 1)
	h.request(t, "GET", "/api/library", nil, 200, &library)
	item := library.Items[0]
	original, err := os.ReadFile(filepath.Join(dir, "packages", item.Id+".ipa"))
	if err != nil {
		t.Fatal(err)
	}
	var shared wire.Share
	h.request(t, "POST", "/api/shares", wire.Selection{Ids: []string{item.Id}}, 201, &shared)
	h.request(t, "POST", "/api/logout", nil, 204, nil)
	h.close()

	// Restart without any Apple credentials or available Apple operations.
	var appleCalls atomic.Int32

	offline := func() error { appleCalls.Add(1); return fmt.Errorf("offline") }

	app, err := server.New(server.Config{DataDir: dir,
		Apple: &testApple{signIn: func(context.Context, string, string, string) (account.Session, error) {
			return nil, offline()
		}, open: func(db.Identity) (account.Session, error) { return nil, offline() }},
		Artwork: func(context.Context, string) ([]byte, error) { return nil, offline() },
	})
	if err != nil {
		t.Fatal(err)
	}
	h = &harness{app: app, http: httptest.NewServer(app.Handler()), dist: httptest.NewServer(app.Distribution()), client: h.client}
	var state wire.State
	h.request(t, "GET", "/api/state", nil, 200, &state)
	h.csrf = state.Csrf
	if state.Account != nil || state.Mode.Public || state.ShareCount != 0 {
		t.Fatalf("guest restored a share from the previous run: %+v", state)
	}
	h.request(t, "GET", "/packages/"+shared.Id+"/"+item.Id+".ipa", nil, 404, nil)
	h.request(t, "GET", "/api/library?q=App&sort=size&direction=desc", nil, 200, &library)
	if len(library.Items) != 1 || library.Total != 1 || library.Items[0].Id != item.Id || !library.Items[0].CanManage || library.Items[0].Icon == "" {
		t.Fatalf("guest library is incomplete: %+v", library)
	}
	h.request(t, "GET", library.Items[0].Icon, nil, 200, nil)
	for _, input := range []wire.Selection{{Filter: &wire.Filter{}}, {Ids: []string{item.Id}}} {
		var selected wire.LibrarySelectionResult
		h.request(t, "POST", "/api/library/selection", input, 200, &selected)
		if len(selected.Ids) != 1 || len(selected.ReadyIds) != 1 || len(selected.ManageIds) != 1 {
			t.Fatalf("guest cannot select the saved IPA: %+v", selected)
		}
	}
	h.request(t, "GET", "/api/catalog", nil, 401, nil)
	h.request(t, "GET", "/api/profile", nil, 401, nil)
	h.request(t, "POST", "/api/catalog/selection", wire.Selection{Filter: &wire.Filter{}}, 401, nil)
	h.request(t, "POST", "/api/downloads", wire.DownloadRequest{Selection: wire.Selection{Ids: []string{"1"}}}, 401, nil)
	var export wire.FileExport
	h.request(t, "POST", "/api/library/export", wire.Selection{Ids: []string{item.Id}}, 201, &export)
	if body := h.request(t, "GET", export.Url, nil, 200, nil); !bytes.Equal(body, original) {
		t.Fatal("guest export changed the IPA")
	}

	checkShare := func(share wire.Share) {
		t.Helper()
		response, err := h.client.Get(h.dist.URL + "/manifests/" + share.Id + ".plist")
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != 200 {
			t.Fatal("guest manifest unavailable")
		}
		body := h.request(t, "GET", "/packages/"+share.Id+"/"+item.Id+".ipa", nil, 200, nil)
		if !bytes.Equal(body, original) {
			t.Fatal("guest share changed the IPA")
		}
	}

	h.request(t, "POST", "/api/shares", wire.Selection{Filter: &wire.Filter{}}, 201, &shared)
	checkShare(shared)
	h.request(t, "POST", "/api/library/delete", wire.Selection{Filter: &wire.Filter{}}, 409, nil)
	h.request(t, "POST", "/api/shares/stop", wire.Selection{Ids: []string{shared.Id}}, 204, nil)
	h.request(t, "POST", "/api/library/delete", wire.Selection{Ids: []string{item.Id}}, 204, nil)
	h.request(t, "GET", "/api/library", nil, 200, &library)
	if library.Total != 0 || len(library.Items) != 0 || appleCalls.Load() != 0 {
		t.Fatalf("guest cleanup failed or contacted Apple: %+v, calls=%d", library, appleCalls.Load())
	}
}

func TestPublicLibraryStillRequiresSignIn(t *testing.T) {
	for _, shared := range []bool{false, true} {
		t.Run(fmt.Sprintf("shared=%t", shared), func(t *testing.T) {
			_, ts, _ := publicAppWithSharedApps(t, shared)
			owner, guest := newBrowser(t, ts), newBrowser(t, ts)
			owner.login("artwork@example.test", "fixture")
			item := owner.download("1")
			selection := wire.Selection{Ids: []string{item.Id}}
			var share wire.Share
			owner.request("POST", "/api/shares", selection, 201, &share)
			var export wire.FileExport
			owner.request("POST", "/api/library/export", selection, 201, &export)
			guest.request("GET", "/api/library", nil, 401, nil)
			guest.request("POST", "/api/library/selection", wire.Selection{Filter: &wire.Filter{}}, 401, nil)
			guest.request("POST", "/api/library/export", selection, 401, nil)
			guest.request("GET", export.Url, nil, 404, nil)
			guest.request("POST", "/api/library/delete", selection, 401, nil)
			guest.request("POST", "/api/shares", selection, 401, nil)
			guest.request("POST", "/api/shares/stop", wire.Selection{Ids: []string{share.Id}}, 401, nil)
			if item.Icon != "" {
				guest.request("GET", item.Icon, nil, 404, nil)
			}
			if guest.state().ShareCount != 0 {
				t.Fatal("public guest received another session's shares")
			}
			owner.request("POST", "/api/logout", nil, 204, nil)
			guest.request("GET", export.Url, nil, 404, nil)
		})
	}
}
