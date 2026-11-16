package session

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/hyperion-cs/appleseum/applekit"

	"appleseum/backend/internal/account"
	"appleseum/backend/internal/db"
	"appleseum/backend/internal/fault"
	"appleseum/backend/internal/library"
	"appleseum/backend/internal/wire"
)

type fixture struct {
	r    *Registry
	s    *db.Db
	conn *sql.DB
	dir  string
}

// newFixture starts a registry on a fresh data directory; conn is a second
// connection for arranging rows the API has no way to write.
func newFixture(t *testing.T, public bool, apple account.Apple) fixture {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "packages"), 0o700); err != nil {
		t.Fatal(err)
	}
	s, err := db.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	report := func(string, error) {}
	lib := library.New(s, func(context.Context, string) ([]byte, error) { return nil, errors.New("offline") }, report, public)
	r, err := New(context.Background(), Config{Public: public, Apple: apple, AllowLogin: func(string) bool { return true }, Report: report,
		Trace: func(ctx context.Context) context.Context { return ctx }}, s, lib)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := sql.Open("sqlite", filepath.Join(dir, "metadata.db")+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close(); r.Close(); s.Close() })
	return fixture{r: r, s: s, conn: conn, dir: dir}
}

func (f fixture) browserSession(t *testing.T, id string, expires time.Time) {
	t.Helper()
	if err := f.s.CreateBrowserSession(context.Background(), id, "synthetic-csrf", expires); err != nil {
		t.Fatal(err)
	}
}

func TestSessionExpiryDoesNotRevokeRenewedSession(t *testing.T) {
	f := newFixture(t, true, nil)
	cutoff := time.Now()
	renewed := cutoff.Add(Lifetime)
	f.browserSession(t, "renewed", renewed)
	if _, err := f.conn.Exec("UPDATE sessions SET secrets=? WHERE id='renewed'", []byte("synthetic-encrypted-secrets")); err != nil {
		t.Fatal(err)
	}
	w, err := f.r.workspace(context.Background(), "renewed")
	if err != nil {
		t.Fatal(err)
	}
	// The sweep chose this session before a browser renewed it.
	f.r.revoke("renewed", cutoff)
	var expiry int64
	var secrets []byte
	if err = f.conn.QueryRow("SELECT expires_at,secrets FROM sessions WHERE id='renewed'").Scan(&expiry, &secrets); err != nil {
		t.Fatal(err)
	}
	if expiry != renewed.Unix() || len(secrets) == 0 || w.ctx.Err() != nil {
		t.Fatal("stale expiry decision revoked a renewed session")
	}
	if _, err = f.conn.Exec("UPDATE sessions SET expires_at=? WHERE id='renewed'", cutoff.Add(-time.Minute).Unix()); err != nil {
		t.Fatal(err)
	}
	f.r.revoke("renewed", cutoff)
	if w.ctx.Err() == nil {
		t.Fatal("expired workspace remains usable")
	}
	if _, err = f.r.workspace(context.Background(), "renewed"); err == nil {
		t.Fatal("revoked session started another workspace")
	}
}

func TestConcurrentWorkspaceCreationAndClose(t *testing.T) {
	f := newFixture(t, true, nil)
	f.browserSession(t, "parallel", time.Now().Add(Lifetime))
	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make(chan *Workspace, 24)
	for range 24 {
		wg.Go(func() {
			<-start
			w, err := f.r.workspace(context.Background(), "parallel")
			if err != nil {
				t.Error(err)
				return
			}
			results <- w
		})
	}
	close(start)
	wg.Wait()
	close(results)
	var first *Workspace
	for w := range results {
		if first == nil {
			first = w
		}
		if w != first {
			t.Fatal("one session got several workspaces")
		}
	}
	for range 4 {
		wg.Go(f.r.Close)
	}
	wg.Wait()
	if _, err := f.r.workspace(context.Background(), "late"); !errors.Is(err, context.Canceled) {
		t.Fatal("closed registry started a workspace", err)
	}
}

func TestIdleWorkspaceReleasesRuntimeWithoutRevokingCookie(t *testing.T) {
	f := newFixture(t, true, nil)
	expires := time.Now().Add(Lifetime)
	f.browserSession(t, "idle", expires)
	w, err := f.r.workspace(context.Background(), "idle")
	if err != nil {
		t.Fatal(err)
	}
	if !w.begin() {
		t.Fatal("new workspace unavailable")
	}
	w.Finish()
	w.mu.Lock()
	w.lastRequest = time.Now().Add(-20 * time.Minute)
	w.mu.Unlock()
	f.r.evictIdle(time.Now())
	if w.ctx.Err() == nil {
		t.Fatal("idle workspace remains active")
	}
	var after int64
	if err = f.conn.QueryRow("SELECT expires_at FROM sessions WHERE id='idle'").Scan(&after); err != nil || after != expires.Unix() {
		t.Fatal("eviction revoked the browser session", err)
	}
	next, err := f.r.workspace(context.Background(), "idle")
	if err != nil || next == w || next.csrf != "synthetic-csrf" {
		t.Fatal("the cookie cannot restore an evicted workspace", err)
	}
	if !next.begin() {
		t.Fatal("restored workspace unavailable")
	}
	f.r.evictIdle(time.Now().Add(time.Hour))
	if next.ctx.Err() != nil {
		t.Fatal("evicted a workspace while it served a request")
	}
	next.Finish()
}

func TestDeviceChangesWaitForSignInAndClearItsState(t *testing.T) {
	f := newFixture(t, false, nil)
	w := f.r.Local()
	ctx := context.Background()
	before, err := w.Device(ctx)
	if err != nil {
		t.Fatal(err)
	}
	busy := func(what string, change func()) {
		t.Helper()
		change()
		if _, err := w.ChangeDevice(ctx, DeviceSettings, before.Settings); !fault.Is(err, fault.Busy) {
			t.Fatal(what, "did not block a device change", err)
		}
	}
	busy("a sign-in", func() { w.mu.Lock(); w.login.busy = true; w.mu.Unlock() })
	w.mu.Lock()
	w.login.busy = false
	// A signed-in account waiting for its verification code keeps its device.
	w.identity, w.login.verification = &db.Identity{Owner: "synthetic-owner"}, true
	w.mu.Unlock()
	busy("a pending verification", func() {})
	w.mu.Lock()
	w.identity = nil
	w.mu.Unlock()
	invalid := before.Settings
	invalid.Locale = "invalid"
	if _, err = w.ChangeDevice(ctx, DeviceSettings, invalid); !fault.Is(err, fault.InvalidDeviceSettings) {
		t.Fatal("invalid settings accepted", err)
	}
	if err = w.apple.Lock(ctx); err != nil {
		t.Fatal(err)
	}
	busy("an Apple operation", func() {})
	w.apple.Unlock()

	w.mu.Lock()
	previous := w.auth
	w.login = loginState{required: true, verification: true, err: fault.VerificationCodeRejected}
	w.mu.Unlock()
	settings := before.Settings
	settings.Locale = "en-GB"
	after, err := w.ChangeDevice(ctx, DeviceSettings, settings)
	if err != nil || after.Id != before.Id {
		t.Fatal("locale change failed or replaced the identity", err)
	}
	state, err := w.State(ctx)
	w.mu.Lock()
	renewed := w.auth != previous
	w.mu.Unlock()
	if err != nil || !renewed || state.Auth != (wire.Auth{State: wire.AuthReady}) {
		t.Fatal("a settings change kept the abandoned sign-in", state, err)
	}
	if reset, err := w.ChangeDevice(ctx, DeviceReset, wire.DeviceSettings{}); err != nil || reset.Id == before.Id {
		t.Fatal("reset kept the identity", err)
	}
}

// signedSession is a saved session whose secrets reopen as a synthetic one.
type signedSession struct {
	account.Session
	identity db.Identity
}

func (s signedSession) OwnerId() string                               { return s.identity.Owner }
func (s signedSession) Account() applekit.Account                     { return s.identity.Account }
func (s signedSession) SessionParameters() applekit.SessionParameters { return s.identity.Parameters }
func (signedSession) SessionSecrets() (applekit.SessionSecrets, error) {
	return applekit.SessionSecrets{PasswordToken: "synthetic-token"}, nil
}

type reopen struct{ opened func(*applekit.Authenticator) }

func (reopen) SignIn(context.Context, *applekit.Authenticator, string, string, string) (account.Session, error) {
	return nil, errors.New("not used")
}

func (o reopen) Open(device *applekit.Authenticator, identity db.Identity, _ applekit.SessionSecrets, _ *http.Client) (account.Session, error) {
	o.opened(device)
	return signedSession{identity: identity}, nil
}

// A sign-in that failed on a draft device must not move the saved session there.
func TestAppleOperationUsesAuthenticatedDeviceAfterUnsuccessfulNewLogin(t *testing.T) {
	var used *applekit.Authenticator
	f := newFixture(t, false, reopen{opened: func(device *applekit.Authenticator) { used = device }})
	w := f.r.Local()
	ctx := context.Background()
	if _, err := w.auth.Device(); err != nil {
		t.Fatal(err)
	}
	active := w.device.Id()
	sealed, err := account.Seal(f.r.localKey, w.Id, account.Secrets{PasswordToken: "synthetic-token"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.conn.Exec(`INSERT INTO accounts(id) VALUES('synthetic-owner');
 UPDATE devices SET account_id='synthetic-owner' WHERE id=?1;
 UPDATE sessions SET secrets=?2 WHERE id='local'`, active, sealed); err != nil {
		t.Fatal(err)
	}
	record, err := f.s.ReadSession(ctx, w.Id)
	if err != nil {
		t.Fatal(err)
	}
	w.mu.Lock()
	w.identity = &record.Identity
	// The draft of the failed sign-in.
	w.device = f.s.Device("", w.Id)
	err = w.renewAuthenticator()
	w.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	release, err := w.Acquire(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if err = w.perform(ctx, func(account.Session) error { return nil }); err != nil {
		t.Fatal(err)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.device.Id() != active || used != w.auth {
		t.Fatal("mixed the session's tokens with the draft device")
	}
}
