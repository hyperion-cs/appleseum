package integration_test

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hyperion-cs/appleseum/applekit"

	"appleseum/backend/internal/account"
	server "appleseum/backend/internal/app"
	"appleseum/backend/internal/db"
	"appleseum/backend/internal/wire"
)

type rememberedApple struct {
	fakeApple
	reject       bool
	failure      error
	profileCalls atomic.Int32
	signedInAt   time.Time
}

func (f *rememberedApple) SessionTimes() (time.Time, time.Time) {
	return f.signedInAt, f.signedInAt
}

func (f *rememberedApple) SessionParameters() applekit.SessionParameters {
	p := f.fakeApple.SessionParameters()
	p.AuthenticatedAt, p.LastSuccessAt = f.SessionTimes()
	return p
}

func (f *rememberedApple) Profile(context.Context) (applekit.AccountProfile, error) {
	f.profileCalls.Add(1)
	if f.failure != nil {
		return applekit.AccountProfile{}, f.failure
	}
	if f.reject {
		return applekit.AccountProfile{}, &applekit.SessionRejection{Operation: "profile", Reason: "authentication", Status: 401}
	}
	return applekit.AccountProfile{Nickname: "renewed"}, nil
}

func (f *rememberedApple) Catalog(ctx context.Context, progress func(float64)) ([]applekit.CatalogApp, error) {
	if f.reject {
		return nil, &applekit.SessionRejection{Operation: "purchases", Reason: "authentication", Status: 401}
	}
	return f.fakeApple.Catalog(ctx, progress)
}

func rememberedHarness(t *testing.T, cfg server.Config) *harness {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(cfg.DataDir, "packages"), 0700); err != nil {
		t.Fatal(err)
	}
	app, err := newTestServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{app: app, http: httptest.NewServer(app.Handler()), client: &http.Client{Timeout: 5 * time.Second}}
	var state wire.State
	h.request(t, "GET", "/api/state", nil, 200, &state)
	h.csrf = state.Csrf
	return h
}

func stopRemembered(h *harness) { h.http.Close(); h.app.Close() }

func credentialCount(t *testing.T, dir string) int {
	t.Helper()
	if localSecrets(t, dir).Password != "" {
		return 1
	}
	return 0
}

func catalogResult(t *testing.T, h *harness) wire.State {
	t.Helper()
	h.request(t, "POST", "/api/catalog/refresh", nil, 202, nil)
	deadline := time.Now().Add(3 * time.Second)
	for {
		var state wire.State
		h.request(t, "GET", "/api/state", nil, 200, &state)
		if state.Catalog.Status != "loading" {
			return state
		}
		if time.Now().After(deadline) {
			t.Fatal("catalog did not finish")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestRememberedCredentialsSurviveRestartAndRenewProfile(t *testing.T) {
	dir := t.TempDir()
	old := &rememberedApple{fakeApple: fakeApple{owner: "remember"}, reject: true, signedInAt: time.Unix(1_700_000_000, 0)}
	fresh := &rememberedApple{fakeApple: fakeApple{owner: "remember"}, signedInAt: old.signedInAt.Add(time.Hour)}
	var logins atomic.Int32
	cfg := server.Config{DataDir: dir, Debug: true, DebugLogDir: filepath.Join(dir, "logs"), Apple: &testApple{signIn: func(_ context.Context, email, password, code string) (account.Session, error) {
		if email != "remember@example.test" || password != "private-password-marker" || code != "" {
			t.Error("wrong remembered credentials")
		}
		if logins.Add(1) == 1 {
			return old, nil
		}
		return fresh, nil
	}, open: func(r db.Identity) (account.Session, error) {
		if r.Parameters.AuthenticatedAt.Equal(fresh.signedInAt) {
			return fresh, nil
		}
		return old, nil
	}}}
	h := rememberedHarness(t, cfg)
	h.request(t, "POST", "/api/login", wire.LoginRequest{Email: "remember@example.test", Password: "private-password-marker", Remember: true}, 200, nil)
	if credentialCount(t, dir) != 1 {
		t.Fatal("credentials not stored")
	}
	stopRemembered(h)
	h = rememberedHarness(t, cfg)
	defer func() { stopRemembered(h) }()
	var profile wire.ProfileSummary
	h.request(t, "GET", "/api/profile", nil, 200, &profile)
	var state wire.State
	h.request(t, "GET", "/api/state", nil, 200, &state)
	if signedInAt(state) != old.signedInAt.UTC().Format(time.RFC3339) || profile.SignedInAt != signedInAt(state) {
		t.Fatal("restored sign-in time changed before renewal", signedInAt(state), profile.SignedInAt)
	}
	if logins.Load() != 1 || old.profileCalls.Load() != 0 || profile.Apple != nil {
		t.Fatal("opening profile triggered remembered login/Apple request")
	}
	body := h.request(t, "POST", "/api/profile/refresh", nil, 200, &profile)
	if profile.Apple == nil || profile.Apple.Nickname != "renewed" || profile.AppleError != "" || logins.Load() != 2 || old.profileCalls.Load() != 1 || fresh.profileCalls.Load() != 1 {
		t.Fatal("profile not transparently renewed", profile, logins.Load())
	}
	h.request(t, "GET", "/api/state", nil, 200, &state)
	if signedInAt(state) != fresh.signedInAt.UTC().Format(time.RFC3339) || profile.SignedInAt != signedInAt(state) {
		t.Fatal("automatic sign-in did not publish its new time", signedInAt(state), profile.SignedInAt)
	}
	stopRemembered(h)

	cfg.Apple.(*testApple).open = func(db.Identity) (account.Session, error) { return fresh, nil }

	h = rememberedHarness(t, cfg)
	h.request(t, "GET", "/api/state", nil, 200, &state)
	if signedInAt(state) != fresh.signedInAt.UTC().Format(time.RFC3339) || logins.Load() != 2 {
		t.Fatal("restart changed renewed sign-in time or signed in again", signedInAt(state))
	}
	h.request(t, "GET", "/api/profile", nil, 200, nil)
	if logins.Load() != 2 || fresh.profileCalls.Load() != 1 {
		t.Fatal("cache made another login")
	}
	for _, path := range []string{"/api/state", "/api/debug", "/api/profile"} {
		body = append(body, h.request(t, "GET", path, nil, 200, nil)...)
	}
	if strings.Contains(string(body), "private-password-marker") {
		t.Fatal("password leaked through API/debug")
	}
	h.request(t, "DELETE", "/api/profile", nil, 204, nil)
	h.request(t, "GET", "/api/profile", nil, 200, &profile)
	if profile.Apple != nil || profile.AppleError != "" || credentialCount(t, dir) != 1 || logins.Load() != 2 {
		t.Fatal("profile clear forgot credentials, fetched Apple or kept cache")
	}
	h.request(t, "POST", "/api/logout", nil, 204, nil)
	if credentialCount(t, dir) != 0 {
		t.Fatal("logout retained credentials")
	}
	h.request(t, "POST", "/api/login", wire.LoginRequest{Email: "remember@example.test", Password: "private-password-marker"}, 200, nil)
	if credentialCount(t, dir) != 0 {
		t.Fatal("unchecked login stored credentials")
	}
}

func TestRememberedCatalogRenewsOnceAndKeepsOwner(t *testing.T) {
	for _, mode := range []string{"success", "still rejected", "wrong owner", "network login", "credentials rejected", "password rejected", "network operation"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			old := &rememberedApple{fakeApple: fakeApple{owner: "same"}, reject: mode != "network operation", signedInAt: time.Unix(1_700_000_000, 0)}
			fresh := &rememberedApple{fakeApple: fakeApple{owner: "same"}, reject: mode == "still rejected", signedInAt: old.signedInAt.Add(time.Hour)}
			if mode == "wrong owner" {
				fresh.owner = "different"
			}
			if mode == "network operation" {
				old.failure = errors.New("network fixture")
			}
			var logins atomic.Int32
			h := rememberedHarness(t, server.Config{DataDir: dir, Apple: &testApple{signIn: func(context.Context, string, string, string) (account.Session, error) {
				if logins.Add(1) == 1 {
					return old, nil
				}
				if mode == "network login" {
					return nil, &applekit.LoginError{Stage: "grandslam", Reason: "network"}
				}
				if mode == "credentials rejected" {
					return nil, applekit.ErrCredentialsRejected
				}
				if mode == "password rejected" {
					return nil, applekit.ErrPasswordRejected
				}
				return fresh, nil
			}}})
			defer stopRemembered(h)
			h.request(t, "POST", "/api/login", wire.LoginRequest{Email: "same@example.test", Password: "fixture", Remember: true}, 200, nil)
			if mode == "network operation" {
				h.request(t, "POST", "/api/profile/refresh", nil, 200, nil)
				if logins.Load() != 1 {
					t.Fatal("network error retried login")
				}
				return
			}
			state := catalogResult(t, h)
			if mode == "password rejected" && (state.Auth.State == wire.AuthReady || state.Auth.Error != "password_rejected") {
				t.Fatal("password rejection did not require corrected sign-in", state)
			}
			if mode == "success" {
				if state.Catalog.Status != "ready" || state.Auth.State != wire.AuthReady {
					t.Fatal("catalog not renewed", state)
				}
			} else {
				if state.Catalog.Status != "failed" || state.Account == nil || state.Account.Id != "same" {
					t.Fatal("failed renewal lost/switches account", state)
				}
				catalogResult(t, h)
			}
			if logins.Load() != 2 {
				t.Fatal("unbounded/missing login", logins.Load())
			}
			expectedTime := old.signedInAt
			if mode == "success" || mode == "still rejected" {
				expectedTime = fresh.signedInAt
			}
			if signedInAt(state) != expectedTime.UTC().Format(time.RFC3339) {
				t.Fatal("sign-in time does not reflect the last installed session", mode, signedInAt(state))
			}
		})
	}
}

func TestRememberedLoginNeedsOnlyNewVerificationCode(t *testing.T) {
	dir := t.TempDir()
	old := &rememberedApple{fakeApple: fakeApple{owner: "verify"}, reject: true, signedInAt: time.Unix(1_700_000_000, 0)}
	fresh := &rememberedApple{fakeApple: fakeApple{owner: "verify"}, signedInAt: old.signedInAt.Add(time.Hour)}
	var logins atomic.Int32
	h := rememberedHarness(t, server.Config{DataDir: dir, Apple: &testApple{signIn: func(_ context.Context, email, password, code string) (account.Session, error) {
		if email != "verify@example.test" || password != "remembered-password" {
			t.Error("lost credentials during verification")
		}
		if logins.Add(1) == 1 {
			return old, nil
		}
		if code == "" {
			return nil, applekit.ErrTwoFactorRequired
		}
		if code != "123456" {
			return nil, applekit.ErrVerificationCodeRejected
		}
		return fresh, nil
	}}})
	defer stopRemembered(h)
	h.request(t, "POST", "/api/login", wire.LoginRequest{Email: "verify@example.test", Password: "remembered-password", Remember: true}, 200, nil)
	h.request(t, "POST", "/api/profile/refresh", nil, 200, nil)
	var state wire.State
	h.request(t, "GET", "/api/state", nil, 200, &state)
	if state.Auth.State == wire.AuthReady || state.Auth.State != wire.AuthCode || logins.Load() != 2 {
		t.Fatal("no code prompt", state)
	}
	if signedInAt(state) != old.signedInAt.UTC().Format(time.RFC3339) {
		t.Fatal("pending verification changed sign-in time", signedInAt(state))
	}
	catalogResult(t, h)
	if logins.Load() != 2 {
		t.Fatal("pending 2FA initiated another login")
	}
	h.request(t, "POST", "/api/login", wire.LoginRequest{Code: "999999"}, 401, nil)
	h.request(t, "GET", "/api/state", nil, 200, &state)
	if state.Auth.State != wire.AuthCode {
		t.Fatal("rejected code discarded continuation")
	}
	if signedInAt(state) != old.signedInAt.UTC().Format(time.RFC3339) {
		t.Fatal("rejected verification changed sign-in time", signedInAt(state))
	}
	h.request(t, "POST", "/api/login", wire.LoginRequest{Code: "123456"}, 200, nil)
	h.request(t, "GET", "/api/state", nil, 200, &state)
	if state.Auth.State != wire.AuthReady || state.Auth.State == wire.AuthCode || state.Account == nil || credentialCount(t, dir) != 1 {
		t.Fatal("code did not complete remembered login", state)
	}
	if signedInAt(state) != fresh.signedInAt.UTC().Format(time.RFC3339) {
		t.Fatal("completed verification did not update sign-in time", signedInAt(state))
	}
}

func TestFailedLoginNeverStoresCredentials(t *testing.T) {
	for _, err := range []error{applekit.ErrTwoFactorRequired, applekit.ErrCredentialsRejected, applekit.ErrPasswordRejected} {
		dir := t.TempDir()
		h := rememberedHarness(t, server.Config{DataDir: dir, Apple: &testApple{signIn: func(context.Context, string, string, string) (account.Session, error) { return nil, err }}})
		h.request(t, "POST", "/api/login", wire.LoginRequest{Email: "fixture@example.test", Password: "not-saved", Remember: true}, 401, nil)
		if credentialCount(t, dir) != 0 {
			t.Fatal("unsuccessful login stored password")
		}
		stopRemembered(h)
	}
}

func TestRememberedConcurrentReadsShareOneRenewal(t *testing.T) {
	old := &rememberedApple{fakeApple: fakeApple{owner: "shared"}, reject: true}
	fresh := &rememberedApple{fakeApple: fakeApple{owner: "shared"}}
	var logins atomic.Int32
	h := rememberedHarness(t, server.Config{DataDir: t.TempDir(), Apple: &testApple{signIn: func(context.Context, string, string, string) (account.Session, error) {
		if logins.Add(1) == 1 {
			return old, nil
		}
		return fresh, nil
	}}})
	defer stopRemembered(h)
	h.request(t, "POST", "/api/login", wire.LoginRequest{Email: "shared@example.test", Password: "fixture", Remember: true}, 200, nil)
	var group sync.WaitGroup
	for range 2 {
		group.Go(func() {
			var p wire.ProfileSummary
			h.request(t, "POST", "/api/profile/refresh", nil, 200, &p)
			if p.Apple == nil || p.AppleError != "" {
				t.Error("concurrent profile did not recover", p)
			}
		})
	}
	group.Wait()
	if logins.Load() != 2 || fresh.profileCalls.Load() != 1 {
		t.Fatal("concurrent reads duplicated renewal/fetch", logins.Load(), fresh.profileCalls.Load())
	}
}

type rememberedDownload struct {
	rememberedApple
	calls atomic.Int32
}

func (f *rememberedDownload) PrepareDownload(ctx context.Context, id int64, versionId string) (applekit.Download, error) {
	f.calls.Add(1)
	if f.reject {
		return nil, &applekit.SessionRejection{Operation: "download", Reason: "token_expired", Status: 200}
	}
	return f.fakeApple.PrepareDownload(ctx, id, versionId)
}

func TestRememberedDownloadRetriesWithFreshSession(t *testing.T) {
	for _, verification := range []bool{false, true} {
		t.Run(map[bool]string{false: "automatic", true: "verification"}[verification], func(t *testing.T) { testRememberedDownload(t, verification) })
	}
}

func testRememberedDownload(t *testing.T, verification bool) {
	dir := t.TempDir()
	old := &rememberedDownload{rememberedApple: rememberedApple{fakeApple: fakeApple{owner: "download"}, reject: true}}
	fresh := &rememberedDownload{rememberedApple: rememberedApple{fakeApple: fakeApple{owner: "download"}}}
	var logins atomic.Int32
	h := rememberedHarness(t, server.Config{DataDir: dir, Apple: &testApple{signIn: func(_ context.Context, _, _, code string) (account.Session, error) {
		if logins.Add(1) == 1 {
			return old, nil
		}
		if verification && code == "" {
			return nil, applekit.ErrTwoFactorRequired
		}
		return fresh, nil
	}}})
	defer stopRemembered(h)
	h.request(t, "POST", "/api/login", wire.LoginRequest{Email: "download@example.test", Password: "fixture", Remember: true}, 200, nil)
	conn, err := sql.Open("sqlite", filepath.Join(dir, "metadata.db"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = conn.Exec("INSERT INTO catalog(id,account_id,app_id,name,delisted) VALUES(?,?,?,?,?)", "01900000-0000-7000-8000-000000000001", "download", 1, "Fixture", false)
	conn.Close()
	if err != nil {
		t.Fatal(err)
	}
	h.request(t, "POST", "/api/downloads", wire.DownloadRequest{Selection: wire.Selection{Ids: []string{"1"}}}, 202, nil)
	if verification {
		deadline := time.Now().Add(3 * time.Second)
		for {
			var state wire.State
			h.request(t, "GET", "/api/state", nil, 200, &state)
			var page wire.LibraryPage
			h.request(t, "GET", "/api/library", nil, 200, &page)
			if state.Auth.State == wire.AuthCode && len(page.Items) == 1 && page.Items[0].Status == "queued" {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("download did not ask for verification")
			}
			time.Sleep(5 * time.Millisecond)
		}
		var page wire.LibraryPage
		h.request(t, "GET", "/api/library", nil, 200, &page)
		if len(page.Items) != 1 || page.Items[0].Status != "queued" || logins.Load() != 2 {
			t.Fatal("pending download was lost/retried", page)
		}
		h.request(t, "POST", "/api/login", wire.LoginRequest{Code: "123456"}, 200, nil)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		var page wire.LibraryPage
		h.request(t, "GET", "/api/library", nil, 200, &page)
		if len(page.Items) > 0 && page.Items[0].Status == "done" {
			break
		}
		if len(page.Items) > 0 && page.Items[0].Status == "failed" {
			t.Fatal("download failed", page.Items[0].Error)
		}
		if time.Now().After(deadline) {
			t.Fatal("download did not finish")
		}
		time.Sleep(5 * time.Millisecond)
	}
	expected := int32(2)
	if verification {
		expected = 3
	}
	if logins.Load() != expected || old.calls.Load() != 1 || fresh.calls.Load() != 1 {
		t.Fatal("wrong download retry counts", logins.Load(), old.calls.Load(), fresh.calls.Load())
	}
}

func TestManualUncheckedLoginRemovesPreviousCredentials(t *testing.T) {
	dir := t.TempDir()
	old := &rememberedApple{fakeApple: fakeApple{owner: "optout"}, reject: true}
	fresh := &rememberedApple{fakeApple: fakeApple{owner: "optout"}}
	var logins atomic.Int32
	h := rememberedHarness(t, server.Config{DataDir: dir, Apple: &testApple{signIn: func(context.Context, string, string, string) (account.Session, error) {
		switch logins.Add(1) {
		case 1:
			return old, nil
		case 2:
			return nil, applekit.ErrCredentialsRejected
		}
		return fresh, nil
	}}})
	defer stopRemembered(h)
	h.request(t, "POST", "/api/login", wire.LoginRequest{Email: "optout@example.test", Password: "old", Remember: true}, 200, nil)
	h.request(t, "POST", "/api/profile/refresh", nil, 200, nil)
	h.request(t, "POST", "/api/login", wire.LoginRequest{Email: "optout@example.test", Password: "new", Remember: false}, 200, nil)
	if credentialCount(t, dir) != 0 {
		t.Fatal("opt-out retained old credentials")
	}
}
