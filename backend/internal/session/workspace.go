package session

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/hyperion-cs/appleseum/applekit"

	"appleseum/backend/internal/account"
	"appleseum/backend/internal/db"
	"appleseum/backend/internal/fault"
	"appleseum/backend/internal/library"
	"appleseum/backend/internal/sharing"
	"appleseum/backend/internal/wire"
)

const (
	catalogIdle = wire.CatalogIdle
)

// Workspace is the server side of one browser session: its Apple account,
// the state of sign-in and catalog refresh, and its download worker.
//
// mu guards the fields below it and is never held across storage, disk or
// network. Operations that must not interleave hold a lock instead: apple for
// this workspace's Apple operations, the registry's device lock for a device.
type Workspace struct {
	r      *Registry
	Id     string
	csrf   string
	ctx    context.Context
	cancel context.CancelFunc
	// Admitted requests and background work; release waits for both.
	wg sync.WaitGroup
	// One Apple operation at a time: sign-in, refresh, versions, download licensing, device changes.
	apple   lock
	wake    chan struct{}
	Exports library.Exports

	mu      sync.Mutex
	service bool
	// identity is the signed-in account; nil when signed out.
	identity *db.Identity
	// accountCtx ends when the account leaves: sign-out, another sign-in, expiry or a replaced device.
	accountCtx    context.Context
	accountCancel context.CancelFunc
	login         loginState
	catalog       catalogState
	device        *db.Device
	auth          *applekit.Authenticator
	// key unlocks the sealed session while a request or background work leases it.
	key         []byte
	leases      int
	workerLease bool
	transport   *http.Client
	requests    int
	lastRequest time.Time
}

type loginState struct {
	// busy: a sign-in is in progress. required: Apple wants the user's input again.
	busy, required, verification bool
	err                          fault.Code
	// Automatic sign-in is not repeated before this moment.
	retryAfter time.Time
}

type catalogState struct {
	status   wire.CatalogStatus
	progress float64
	err      fault.Code
}

// Actor is who makes a request, read once when the request starts.
type Actor struct {
	Owner    string // Apple account id; "" when nobody is signed in
	Email    string
	SignedIn bool
	Service  bool
	Public   bool
	Shared   bool
}

func (w *Workspace) Csrf() string { return w.csrf }

func (w *Workspace) Actor() Actor {
	w.mu.Lock()
	defer w.mu.Unlock()
	actor := Actor{Service: w.service, Public: w.r.cfg.Public, Shared: w.r.cfg.SharedLibrary}
	if w.identity != nil {
		actor.Owner, actor.Email, actor.SignedIn = w.identity.Owner, w.identity.Account.Email, true
	}
	return actor
}

func (a Actor) Scope() db.Scope {
	return db.Scope{Owner: a.Owner, Public: a.Public, Service: a.Service, SharedLibrary: a.Shared}
}

// Library reports whether the library is open to the actor: to anyone
// locally, to a signed-in or the service account publicly.
func (a Actor) Library() bool { return !a.Public || a.SignedIn || a.Service }

// ShareOwner is who the actor's shares belong to; it survives sign-out and session expiry.
func (a Actor) ShareOwner() sharing.Owner {
	switch {
	case !a.Public:
		return sharing.Owner{Id: "local"}
	case a.Service:
		return sharing.Owner{Id: "service", Name: ServiceLogin}
	case !a.SignedIn:
		return sharing.Owner{}
	}
	return sharing.Owner{Id: "account:" + a.Owner, Name: a.Email}
}

// ShareScope is the visibility of shares: by share owner rather than by library owner.
func (a Actor) ShareScope() db.Scope {
	scope := a.Scope()
	scope.Owner = a.ShareOwner().Id
	return scope
}

// begin admits a request unless the workspace has stopped.
func (w *Workspace) begin() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.ctx.Err() != nil {
		return false
	}
	w.wg.Add(1)
	w.requests++
	w.lastRequest = time.Now()
	return true
}

// Finish ends a request admitted by the registry.
func (w *Workspace) Finish() {
	w.mu.Lock()
	w.requests--
	w.lastRequest = time.Now()
	w.mu.Unlock()
	w.wg.Done()
}

// Context ends when the workspace stops.
func (w *Workspace) Context() context.Context { return w.ctx }

// evictIfIdle stops a workspace nobody uses and reports whether it did.
func (w *Workspace) evictIfIdle(now time.Time) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	idle := !w.service && w.requests == 0 && w.leases == 0 && !w.login.busy && !w.lastRequest.IsZero() &&
		now.Sub(w.lastRequest) >= workspaceIdleTimeout && !w.auth.VerificationPending()
	// Leave the registry in the same step, so no request is admitted in between.
	if idle && w.r.remove(w) {
		w.cancel()
		w.accountCancel()
		return true
	}
	return false
}

func (w *Workspace) stop() {
	w.cancel()
	w.mu.Lock()
	w.accountCancel()
	w.mu.Unlock()
}

// release waits for the stopped workspace's work and wipes what it kept in memory.
func (w *Workspace) release() {
	w.wg.Wait()
	w.mu.Lock()
	defer w.mu.Unlock()
	w.forgetSecrets()
	if w.transport != nil {
		w.transport.CloseIdleConnections()
	}
	w.auth.CancelVerification()
}

// renewAuthenticator replaces the device's sign-in state, dropping a pending verification.
func (w *Workspace) renewAuthenticator() error {
	var transport http.RoundTripper
	if w.transport != nil {
		transport = w.transport.Transport
	}
	auth, err := applekit.NewAuthenticator(applekit.AuthenticatorOptions{Store: w.device, Transport: transport})
	if err == nil {
		w.auth = auth
	}
	return err
}

// Acquire unlocks the saved session for one request: with the browser's key
// in public mode, with local.key otherwise. Without a key the request simply
// runs without secrets. Release when the request ends.
func (w *Workspace) Acquire(ctx context.Context, key []byte) (release func(), err error) {
	if !w.r.cfg.Public {
		key = w.r.localKey
	}
	w.mu.Lock()
	service, leased := w.service, w.leases > 0
	w.mu.Unlock()
	if service || len(key) == 0 {
		return func() {}, nil
	}
	if len(key) != account.KeySize {
		return nil, errors.New("invalid session key")
	}
	var queued bool
	if !leased {
		if err = w.verifyKey(ctx, key); err != nil {
			return nil, err
		}
		queued, err = w.r.db.Queued(ctx, w.Id)
		if err != nil {
			return nil, err
		}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.leases > 0 && subtle.ConstantTimeCompare(key, w.key) != 1 {
		return nil, account.ErrKey
	}
	if w.leases == 0 {
		w.key = append([]byte(nil), key...)
	}
	w.leases++
	if queued && w.identity != nil && !w.login.required {
		w.wakeWorker()
	}
	return w.releaseSecrets, nil
}

// verifyKey checks a key against the saved session, if there is one.
func (w *Workspace) verifyKey(ctx context.Context, key []byte) error {
	sealed, err := w.r.db.SealedSession(ctx, w.Id)
	if err != nil || len(sealed) == 0 {
		return err
	}
	_, err = account.Open(key, w.Id, sealed)
	return err
}

// Unlocked reports whether a request or background work holds the session key.
func (w *Workspace) Unlocked() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.key) == account.KeySize
}

// retainSecrets extends the lease to background work; requires mu.
func (w *Workspace) retainSecrets() bool {
	if w.leases == 0 {
		return false
	}
	w.leases++
	return true
}

func (w *Workspace) releaseSecrets() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.releaseSecretsLocked()
}

// Requires mu.
func (w *Workspace) releaseSecretsLocked() {
	if w.leases--; w.leases == 0 {
		w.forgetSecrets()
	}
}

// Requires mu.
func (w *Workspace) forgetSecrets() {
	clear(w.key)
	w.key = nil
}

func (w *Workspace) sessionKey() []byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]byte(nil), w.key...)
}

// State is what the interface polls: modes, the account, sign-in and catalog progress.
func (w *Workspace) State(ctx context.Context) (wire.State, error) {
	cfg := w.r.cfg
	w.mu.Lock()
	identity := w.identity
	state := wire.State{
		Csrf:    w.csrf,
		Mode:    wire.Mode{Public: cfg.Public, SharedLibrary: cfg.SharedLibrary, Service: w.service, Admin: cfg.Admin && !w.service, Debug: cfg.Debug, Ish: cfg.Ish},
		Auth:    wire.Auth{State: w.login.state(), Busy: w.login.busy || identity != nil && w.auth.VerificationPending(), Error: w.login.err},
		Catalog: wire.Catalog{Status: w.catalog.status, Progress: w.catalog.progress, Error: w.catalog.err},
	}
	w.mu.Unlock()
	actor := Actor{Service: state.Mode.Service, Public: cfg.Public, Shared: cfg.SharedLibrary}
	var err error
	if state.Tables, err = w.r.db.Tables(ctx, w.Id); err != nil {
		return state, err
	}
	if state.Preferences, err = w.r.db.Preferences(ctx, w.Id); err != nil {
		return state, err
	}
	if identity != nil {
		actor.Owner, actor.Email, actor.SignedIn = identity.Owner, identity.Account.Email, true
		summary := AccountSummary(*identity)
		state.Account = &summary
		if state.Catalog.Cached, err = w.r.db.HasCatalog(ctx, identity.Owner); err != nil {
			return state, err
		}
	}
	if actor.Library() {
		if state.DownloadsActive, err = w.r.db.HasActiveDownloads(ctx, actor.Scope()); err != nil {
			return state, err
		}
	}
	state.StorageError = w.r.library.StorageError()
	state.ShareCount, err = w.r.db.ShareCount(ctx, actor.ShareScope())
	return state, err
}

// state names what signing in waits for; a verification code implies the sign-in itself.
func (l loginState) state() wire.AuthState {
	switch {
	case l.verification:
		return wire.AuthCode
	case l.required:
		return wire.AuthPassword
	}
	return wire.AuthReady
}

func AccountSummary(identity db.Identity) wire.Account {
	return wire.Account{
		Email:      identity.Account.Email,
		Name:       identity.Account.Name,
		Id:         identity.Owner,
		Country:    identity.Account.Country,
		Storefront: identity.Account.StoreFront,
		SignedInAt: account.SignedInAt(identity),
	}
}
