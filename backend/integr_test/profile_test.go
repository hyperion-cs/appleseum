package integration_test

import (
	"context"
	"database/sql"
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

type profileFixture struct {
	fakeApple
	calls    *atomic.Int64
	rejected atomic.Bool
}

func (f *profileFixture) Profile(context.Context) (applekit.AccountProfile, error) {
	f.calls.Add(1)
	if f.rejected.Load() {
		return applekit.AccountProfile{}, &applekit.SessionRejection{Operation: "profile", Reason: "authentication", Status: 401}
	}
	zero := int64(0)
	return applekit.AccountProfile{Nickname: f.owner, FamilyRole: "member", Subscriptions: &zero}, nil
}

func (f *profileFixture) SessionSecrets() (applekit.SessionSecrets, error) {
	token := "before-profile"
	if f.calls.Load() > 0 {
		token = "cookie-after-profile"
	}
	return applekit.SessionSecrets{PasswordToken: token}, nil
}

func TestProfileCachesPerSessionAndKeepsSessionOnRejection(t *testing.T) {
	for _, rejected := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "scoped rejection"}[rejected], func(t *testing.T) {
			dir := t.TempDir()
			if err := os.MkdirAll(filepath.Join(dir, "packages"), 0700); err != nil {
				t.Fatal(err)
			}
			var calls atomic.Int64
			app, err := newTestServer(server.Config{DataDir: dir, Apple: &testApple{signIn: func(_ context.Context, email, _, _ string) (account.Session, error) {
				fixture := &profileFixture{fakeApple: fakeApple{owner: email}, calls: &calls}
				fixture.rejected.Store(rejected)
				return fixture, nil
			}}})
			if err != nil {
				t.Fatal(err)
			}
			h := &harness{app: app, http: httptest.NewServer(app.Handler()), dist: httptest.NewServer(app.Distribution()), client: &http.Client{Timeout: 5 * time.Second}}
			defer h.close()
			var state wire.State
			h.request(t, "GET", "/api/state", nil, 200, &state)
			h.csrf = state.Csrf
			h.request(t, "POST", "/api/login", wire.LoginRequest{Email: "first@example.test", Password: "fixture"}, 200, nil)
			h.refresh(t)
			var profile wire.ProfileSummary
			h.request(t, "GET", "/api/profile", nil, 200, &profile)
			if calls.Load() != 0 || profile.Apple != nil {
				t.Fatal("opening an empty profile contacted Apple")
			}
			for _, method := range []string{"GET", "GET", "POST"} {
				path := "/api/profile"
				if method == "POST" {
					path += "/refresh"
				}
				h.request(t, method, path, nil, 200, &profile)
			}
			if calls.Load() != 1 || profile.CatalogApps != 32 {
				t.Fatal("profile repeated calls or lost local catalog", calls.Load(), profile)
			}
			if rejected {
				if profile.Apple != nil || profile.AppleError != "profile_authentication_required" {
					t.Fatal("scoped rejection hidden or mislabeled", profile)
				}
			} else if profile.Apple == nil || profile.Apple.Nickname != "first@example.test" || profile.Apple.Subscriptions == nil || *profile.Apple.Subscriptions != 0 {
				t.Fatal("profile lost account data or zero count", profile)
			}
			h.request(t, "GET", "/api/state", nil, 200, &state)
			if state.Account == nil || state.Catalog.Status != "ready" {
				t.Fatal("profile failure invalidated store login or catalog")
			}
			if localSecrets(t, dir).PasswordToken != "cookie-after-profile" {
				t.Fatal("response cookies lost")
			}

			req, _ := http.NewRequest("POST", h.http.URL+"/api/profile/refresh", nil)
			resp, err := h.client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != 403 || calls.Load() != 1 {
				t.Fatal("refresh bypassed CSRF or contacted Apple")
			}
			h.request(t, "POST", "/api/logout", nil, 204, nil)
			h.request(t, "POST", "/api/login", wire.LoginRequest{Email: "second@example.test", Password: "fixture"}, 200, nil)
			h.request(t, "GET", "/api/profile", nil, 200, &profile)
			if calls.Load() != 1 || profile.Apple != nil {
				t.Fatal("another login fetched/reused another account's profile")
			}
			h.request(t, "POST", "/api/profile/refresh", nil, 200, &profile)
			if calls.Load() != 2 || profile.CatalogApps != 0 {
				t.Fatal("cache reused another login or catalog", calls.Load(), profile)
			}
			if !rejected && (profile.Apple == nil || profile.Apple.Nickname != "second@example.test") {
				t.Fatal("another account's profile leaked")
			}
		})
	}
}

func TestProfilePersistsPerOwnerWithoutAutomaticRefresh(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "packages"), 0700); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int64
	first := &profileFixture{fakeApple: fakeApple{owner: "first"}, calls: &calls}
	second := &profileFixture{fakeApple: fakeApple{owner: "second"}, calls: &calls}
	cfg := server.Config{DataDir: dir, Apple: &testApple{signIn: func(_ context.Context, email, _, _ string) (account.Session, error) {
		if email == "first@example.test" {
			return first, nil
		}
		return second, nil
	}, open: func(r db.Identity) (account.Session, error) {
		if r.Owner == "first" {
			return first, nil
		}
		return second, nil
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
	h.request(t, "POST", "/api/login", wire.LoginRequest{Email: "first@example.test", Password: "fixture"}, 200, nil)
	h.refresh(t)
	var p wire.ProfileSummary
	h.request(t, "POST", "/api/profile/refresh", nil, 200, &p)
	if p.Apple == nil || p.Apple.Nickname != "first" || calls.Load() != 1 {
		t.Fatal("manual refresh did not cache profile", p)
	}
	conn, err := sql.Open("sqlite", filepath.Join(dir, "metadata.db"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = conn.Exec("UPDATE accounts SET profile_attempted_at='2000-01-01T00:00:00Z'")
	conn.Close()
	if err != nil {
		t.Fatal(err)
	}
	first.rejected.Store(true)
	h.close()
	h = start()
	for range 2 {
		h.request(t, "GET", "/api/profile", nil, 200, &p)
		if p.Apple == nil || p.Apple.Nickname != "first" || p.AppleError != "" || calls.Load() != 1 {
			t.Fatal("restart/old cache triggered automatic refresh", p, calls.Load())
		}
	}
	h.request(t, "POST", "/api/profile/refresh", nil, 200, &p)
	if p.Apple == nil || p.Apple.Nickname != "first" || p.AppleError != "profile_authentication_required" || calls.Load() != 2 {
		t.Fatal("failed refresh discarded old profile", p)
	}
	h.request(t, "GET", "/api/profile", nil, 200, &p)
	if calls.Load() != 2 {
		t.Fatal("read retried a failed refresh")
	}
	h.request(t, "POST", "/api/logout", nil, 204, nil)
	h.request(t, "POST", "/api/login", wire.LoginRequest{Email: "second@example.test", Password: "fixture"}, 200, nil)
	h.request(t, "GET", "/api/profile", nil, 200, &p)
	if p.Apple != nil || p.AppleError != "" || calls.Load() != 2 {
		t.Fatal("cache leaked between owners", p)
	}
	h.request(t, "POST", "/api/profile/refresh", nil, 200, &p)
	if p.Apple == nil || p.Apple.Nickname != "second" {
		t.Fatal("second owner profile did not refresh")
	}
	h.request(t, "POST", "/api/logout", nil, 204, nil)
	h.request(t, "POST", "/api/login", wire.LoginRequest{Email: "first@example.test", Password: "fixture"}, 200, nil)
	h.request(t, "GET", "/api/profile", nil, 200, &p)
	if p.Apple == nil || p.Apple.Nickname != "first" || p.AppleError != "" || calls.Load() != 3 {
		t.Fatal("relogin discarded the account cache or fetched from Apple", p)
	}
	req, _ := http.NewRequest(http.MethodDelete, h.http.URL+"/api/profile", nil)
	resp, err := h.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatal("clear bypassed CSRF")
	}
	h.request(t, "GET", "/api/profile", nil, 200, &p)
	if p.Apple == nil {
		t.Fatal("unauthorized clear removed cache")
	}
	for range 2 {
		h.request(t, "DELETE", "/api/profile", nil, 204, nil)
	}
	h.request(t, "GET", "/api/profile", nil, 200, &p)
	if p.Apple != nil || p.AppleError != "" || calls.Load() != 3 || p.CatalogApps != 32 {
		t.Fatal("clear did not restore empty state or fetched Apple", p)
	}
	h.request(t, "POST", "/api/profile/refresh", nil, 200, &p)
	if p.Apple != nil || p.AppleError != "profile_authentication_required" || calls.Load() != 4 {
		t.Fatal("empty failed refresh not handled", p)
	}
	h.request(t, "DELETE", "/api/profile", nil, 204, nil)
	h.request(t, "GET", "/api/profile", nil, 200, &p)
	if p.Apple != nil || p.AppleError != "" || calls.Load() != 4 {
		t.Fatal("clear retained failure or auto-refreshed", p)
	}
	h.request(t, "POST", "/api/logout", nil, 204, nil)
	h.request(t, "POST", "/api/login", wire.LoginRequest{Email: "second@example.test", Password: "fixture"}, 200, nil)
	h.request(t, "GET", "/api/profile", nil, 200, &p)
	if p.Apple == nil || p.Apple.Nickname != "second" || calls.Load() != 4 {
		t.Fatal("clear affected another owner", p)
	}
}
