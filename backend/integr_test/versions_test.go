package integration_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hyperion-cs/appleseum/applekit"

	"appleseum/backend/internal/account"
	server "appleseum/backend/internal/app"
	"appleseum/backend/internal/wire"
)

type versionsApple struct {
	*fakeApple
	mu         sync.Mutex
	listed     int
	resolved   []string
	downloaded chan string
	info       func(context.Context, string) (applekit.VersionMetadata, error)
	tracked    func(context.Context, string, func(float64)) (applekit.VersionMetadata, error)
}

func (f *versionsApple) Versions(context.Context, int64) ([]string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listed++
	ids := make([]string, 520)
	for i := range ids {
		ids[i] = strconv.Itoa(1_000 + i)
	}
	return ids, "1519", nil
}

func (f *versionsApple) versionInfo(ctx context.Context, appId int64, id string) (applekit.VersionMetadata, error) {
	f.mu.Lock()
	f.resolved = append(f.resolved, id)
	f.mu.Unlock()
	if f.info != nil {
		return f.info(ctx, id)
	}
	return applekit.VersionMetadata{Version: "2." + id, Build: id, MinimumOs: "16.0"}, nil
}

func (f *versionsApple) PrepareDownload(ctx context.Context, appId int64, id string) (applekit.Download, error) {
	f.downloaded <- id
	return f.fakeApple.PrepareDownload(ctx, appId, id)
}

func (f *versionsApple) VersionInfo(ctx context.Context, appId int64, id string, progress func(float64)) (applekit.VersionMetadata, error) {
	if f.tracked != nil {
		return f.tracked(ctx, id, progress)
	}
	result, err := f.versionInfo(ctx, appId, id)
	if err == nil {
		for _, percent := range []float64{25, 75, 100} {
			progress(percent)
		}
	}
	return result, err
}

func launchVersions(t *testing.T, debug ...bool) (*harness, *versionsApple) {
	dir := t.TempDir()
	os.MkdirAll(dir+"/packages", 0700)
	fake := &versionsApple{fakeApple: &fakeApple{owner: "versions"}, downloaded: make(chan string, 1)}
	app, err := newTestServer(server.Config{DataDir: dir, Debug: len(debug) > 0 && debug[0], Apple: &testApple{signIn: func(context.Context, string, string, string) (account.Session, error) { return fake, nil }}})
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{app: app, http: httptest.NewServer(app.Handler()), dist: httptest.NewServer(app.Distribution()), client: &http.Client{Timeout: 5 * time.Second}}
	var state wire.State
	h.request(t, "GET", "/api/state", nil, 200, &state)
	h.csrf = state.Csrf
	h.request(t, "POST", "/api/login", wire.LoginRequest{Email: "versions@example.test", Password: "synthetic"}, 200, &state)
	t.Cleanup(h.close)
	h.refresh(t)
	return h, fake
}

func TestVersionPageDefaultsToFive(t *testing.T) {
	h, fake := launchVersions(t)
	var page wire.VersionPage
	h.request(t, "POST", "/api/catalog/1/versions", wire.VersionPageRequest{}, 200, &page)
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(page.Items) != 5 || len(fake.resolved) != 5 {
		t.Fatalf("default page loaded %d rows and resolved %d versions", len(page.Items), len(fake.resolved))
	}
}

func TestVersionPagesResolveOnlyVisibleRowsAndDownloadSelection(t *testing.T) {
	h, fake := launchVersions(t)
	var first, second, previous wire.VersionPage
	h.request(t, "POST", "/api/catalog/1/versions", wire.VersionPageRequest{Limit: 2}, 200, &first)
	if first.Total != 520 || len(first.Items) != 2 || first.Items[0].Id != "1519" || first.Items[1].Id != "1518" || !first.Items[0].Latest || first.Items[0].MinimumOs != "16.0" || !first.Next || first.Previous {
		t.Fatalf("first page %+v", first)
	}
	h.request(t, "POST", "/api/catalog/1/versions", wire.VersionPageRequest{Limit: 2, Cursor: first.Items[len(first.Items)-1].Cursor, Revision: first.Revision}, 200, &second)
	if second.Items[0].Id != "1517" || !second.Previous {
		t.Fatal(second)
	}
	h.request(t, "POST", "/api/catalog/1/versions", wire.VersionPageRequest{Limit: 2, Cursor: second.Items[0].Cursor, Move: "prev", Revision: second.Revision}, 200, &previous)
	fake.mu.Lock()
	listed, resolved := fake.listed, len(fake.resolved)
	fake.mu.Unlock()
	if listed != 1 || resolved != 4 {
		t.Fatalf("list=%d resolved=%d, expected 1 and 4", listed, resolved)
	}
	h.request(t, "POST", "/api/catalog/2/versions", wire.VersionPageRequest{Limit: 2, Cursor: first.Items[len(first.Items)-1].Cursor, Revision: first.Revision}, 409, nil)
	h.request(t, "POST", "/api/catalog/9999/versions", wire.VersionPageRequest{Limit: 2}, 404, nil)
	h.request(t, "POST", "/api/catalog/1/versions", wire.VersionPageRequest{Limit: 129}, 400, nil)
	h.request(t, "POST", "/api/downloads", wire.DownloadRequest{Selection: wire.Selection{Ids: []string{"1"}}, Versions: []wire.DownloadVersion{{AppId: "1", VersionId: "1000"}}}, 400, nil)
	h.request(t, "POST", "/api/downloads", wire.DownloadRequest{Selection: wire.Selection{Ids: []string{"1"}}, Versions: []wire.DownloadVersion{{AppId: "2", VersionId: "1518"}}}, 400, nil)
	h.request(t, "POST", "/api/downloads", wire.DownloadRequest{Selection: wire.Selection{Ids: []string{"1"}}, Versions: []wire.DownloadVersion{{AppId: "1", VersionId: "1518"}}}, 202, nil)
	select {
	case got := <-fake.downloaded:
		if got != "1518" {
			t.Fatal(got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal(fmt.Errorf("selected version was not dispatched"))
	}
	h.waitDone(t, 1)
	var library wire.LibraryPage
	h.request(t, "GET", "/api/library", nil, 200, &library)
	if len(library.Items) != 1 || library.Items[0].Version != "1.2.3" {
		t.Fatalf("selected-version download must display the version from the IPA: %+v", library)
	}
}

func TestVersionPageCancellationStopsResolving(t *testing.T) {
	h, fake := launchVersions(t)
	started, canceled := make(chan struct{}), make(chan struct{})

	fake.info = func(ctx context.Context, id string) (applekit.VersionMetadata, error) {
		close(started)
		<-ctx.Done()
		close(canceled)
		return applekit.VersionMetadata{}, ctx.Err()
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "POST", h.http.URL+"/api/catalog/1/versions", strings.NewReader(`{"limit":10}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-CSRF-Token", h.csrf)
	req.Header.Set("Content-Type", "application/json")
	done := make(chan struct{})
	go func() {
		defer close(done)
		res, _ := h.client.Do(req)
		if res != nil {
			res.Body.Close()
		}
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("resolver did not start")
	}
	cancel()
	select {
	case <-canceled:
	case <-time.After(2 * time.Second):
		t.Fatal("resolver did not cancel")
	}
	<-done
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.resolved) != 1 {
		t.Fatalf("resolved %d rows after cancellation", len(fake.resolved))
	}
}

func TestVersionPageRetriesOnlyFailedRows(t *testing.T) {
	h, fake := launchVersions(t)
	attempts := 0

	fake.info = func(ctx context.Context, id string) (applekit.VersionMetadata, error) {
		if id == "1519" {
			attempts++
			if attempts == 1 {
				return applekit.VersionMetadata{}, errors.New("temporary failure")
			}
		}
		return applekit.VersionMetadata{Version: "2." + id, MinimumOs: "16.0"}, nil
	}

	var first, retry wire.VersionPage
	h.request(t, "POST", "/api/catalog/1/versions", wire.VersionPageRequest{Limit: 2}, 200, &first)
	if first.Items[0].Error != "version_unavailable" || first.Items[1].Version == "" {
		t.Fatal(first)
	}
	h.request(t, "POST", "/api/catalog/1/versions", wire.VersionPageRequest{Limit: 2}, 200, &retry)
	if retry.Items[0].Error != "" || retry.Items[0].Version == "" {
		t.Fatal(retry)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.listed != 1 || len(fake.resolved) != 3 {
		t.Fatalf("listed %d resolved %d", fake.listed, len(fake.resolved))
	}
}
