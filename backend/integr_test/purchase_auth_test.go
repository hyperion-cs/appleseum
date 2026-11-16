package integration_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hyperion-cs/appleseum/applekit"

	"appleseum/backend/internal/account"
	server "appleseum/backend/internal/app"
	"appleseum/backend/internal/db"
	"appleseum/backend/internal/wire"
)

type historyRejection struct {
	fakeApple
	rejected atomic.Bool
	status   int
}

func (f *historyRejection) Catalog(ctx context.Context, progress func(float64)) ([]applekit.CatalogApp, error) {
	if f.rejected.Load() {
		return nil, &applekit.SessionRejection{Operation: "purchases", Reason: "authentication", Status: f.status, AgeKnown: true, Age: 3 * time.Hour, Idle: 25 * time.Minute}
	}
	return f.fakeApple.Catalog(ctx, progress)
}

func (f *historyRejection) SessionSecrets() (applekit.SessionSecrets, error) {
	token := "before-history"
	if f.rejected.Load() {
		token = "cookie-after-history-rejection"
	}
	return applekit.SessionSecrets{PasswordToken: token}, nil
}

func TestPurchaseAuthenticationRejectionKeepsSessionCatalogAndDownloads(t *testing.T) {
	for _, status := range []int{401, 403} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			dir := t.TempDir()
			if err := os.MkdirAll(filepath.Join(dir, "packages"), 0700); err != nil {
				t.Fatal(err)
			}
			client := &historyRejection{fakeApple: fakeApple{owner: "history-scope"}, status: status}
			cfg := server.Config{DataDir: dir, Apple: &testApple{signIn: func(context.Context, string, string, string) (account.Session, error) { return client, nil }, open: func(saved db.Identity) (account.Session, error) {
				if saved.Owner != "history-scope" {
					t.Fatal("response cookie was not persisted")
				}
				return client, nil
			}}}

			start := func() *harness {
				app, err := newTestServer(cfg)
				if err != nil {
					t.Fatal(err)
				}
				h := &harness{app: app, http: httptest.NewServer(app.Handler()), dist: httptest.NewServer(app.Distribution()), client: &http.Client{Timeout: 5 * time.Second}}
				var state wire.State
				h.request(t, "GET", "/api/state", nil, 200, &state)
				h.csrf = state.Csrf
				return h
			}

			h := start()
			defer func() {
				if h != nil {
					h.close()
				}
			}()
			h.request(t, "POST", "/api/login", wire.LoginRequest{Email: "synthetic@example.test", Password: "fixture"}, 200, nil)
			h.refresh(t)
			var before wire.CatalogPage
			h.request(t, "GET", "/api/catalog", nil, 200, &before)
			client.rejected.Store(true)
			h.request(t, "POST", "/api/catalog/refresh", nil, 202, nil)
			deadline := time.Now().Add(3 * time.Second)
			for {
				var state wire.State
				h.request(t, "GET", "/api/state", nil, 200, &state)
				if state.Catalog.Status == "failed" {
					if state.Account == nil || !state.Catalog.Cached || state.Catalog.Error != "purchase_history_authentication_required" {
						t.Fatal("history failure discarded account or cache", state)
					}
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("history did not fail")
				}
				time.Sleep(10 * time.Millisecond)
			}
			var after wire.CatalogPage
			h.request(t, "GET", "/api/catalog", nil, 200, &after)
			if after.Total != before.Total || after.Total == 0 {
				t.Fatal("cached history was replaced")
			}
			if localSecrets(t, dir).PasswordToken != "cookie-after-history-rejection" {
				t.Fatal("response cookies lost")
			}

			h.close()
			h = start()
			if localSecrets(t, dir).PasswordToken != "cookie-after-history-rejection" {
				t.Fatal("session was not restored")
			}
			h.request(t, "POST", "/api/downloads", wire.DownloadRequest{Selection: wire.Selection{Ids: []string{"31"}}}, 202, nil)
			deadline = time.Now().Add(3 * time.Second)
			for {
				var jobs wire.LibraryPage
				h.request(t, "GET", "/api/library", nil, 200, &jobs)
				if len(jobs.Items) == 1 && jobs.Items[0].Status == "done" {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("history failure prevented download", jobs)
				}
				time.Sleep(10 * time.Millisecond)
			}
		})
	}
}
