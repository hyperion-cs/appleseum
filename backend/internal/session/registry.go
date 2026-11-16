package session

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"sync"
	"time"

	"appleseum/backend/internal/account"
	"appleseum/backend/internal/db"
	"appleseum/backend/internal/fault"
	"appleseum/backend/internal/library"
)

const (
	Lifetime     = 30 * 24 * time.Hour
	ServiceLogin = "root@appleseum"
)

const (
	maxActiveWorkspaces  = 1_024
	sweepInterval        = time.Minute
	workspaceIdleTimeout = 15 * time.Minute
	localId              = "local"
	maxAdmissionAttempts = 2
)

type Config struct {
	Public, SharedLibrary, Admin, Debug, Ish bool
	// Apple signs in and reopens sessions; nil talks to Apple.
	Apple account.Apple
	// AllowLogin limits sign-in attempts per login name.
	AllowLogin func(login string) bool
	// Report records a local failure for diagnostics.
	Report func(operation string, err error)
	// Trace attaches diagnostics of Apple requests to a request's context.
	Trace func(context.Context) context.Context
}

// Registry owns the workspaces: one per browser session in public mode, a
// single local one otherwise. Workspaces affect each other only through it.
type Registry struct {
	cfg     Config
	db      *db.Db
	library *library.Service
	ctx     context.Context
	cancel  context.CancelFunc
	// Background work that outlives a workspace, such as releasing its secrets.
	wg     sync.WaitGroup
	closed sync.Once

	localKey     []byte
	rootPassword string
	local        *Workspace

	mu         sync.Mutex
	workspaces map[string]*Workspace
	devices    map[string]lock
}

// New prepares the registry. In local mode it opens the only workspace and
// checks that local.key still unlocks the saved session.
func New(ctx context.Context, cfg Config, s *db.Db, lib *library.Service) (*Registry, error) {
	if cfg.Apple == nil {
		cfg.Apple = account.AppleKit{}
	}
	r := &Registry{cfg: cfg, db: s, library: lib, workspaces: make(map[string]*Workspace), devices: make(map[string]lock)}
	r.ctx, r.cancel = context.WithCancel(ctx)
	r.wg.Go(r.recoverTransfers)
	if cfg.Public {
		r.rootPassword = account.RandomPassword()
		r.wg.Go(r.sweep)
		return r, nil
	}
	required, err := s.HasLocalSecrets(ctx)
	if err == nil {
		r.localKey, err = account.LoadLocalKey(s.Directory(), required)
	}
	if err == nil {
		err = s.EnsureLocalSession(ctx, account.RandomToken())
	}
	if err == nil {
		r.local, err = r.workspace(ctx, localId)
	}
	if err == nil {
		var release func()
		if release, err = r.local.Acquire(ctx, nil); err == nil {
			release()
		}
	}
	if err != nil {
		r.Close()
		return nil, err
	}
	return r, nil
}

func (r *Registry) ServicePassword() string { return r.rootPassword }

func (r *Registry) serviceLogin(password string) bool {
	return subtle.ConstantTimeCompare([]byte(password), []byte(r.rootPassword)) == 1
}

// Local is the single workspace of local mode.
func (r *Registry) Local() *Workspace { return r.local }

// Resume finds the workspace of a browser's session token, extending the
// session, or starts a new session when the token is unknown or expired.
// It returns the token the browser must keep and whether it is a new one.
func (r *Registry) Resume(ctx context.Context, token string) (w *Workspace, kept string, fresh bool, err error) {
	now := time.Now()
	if len(token) == base64.RawURLEncoding.EncodedLen(account.TokenSize) {
		id := account.TokenHash(token)
		renewed, err := r.db.RefreshBrowserSession(ctx, id, now, now.Add(Lifetime))
		if err != nil {
			return nil, "", false, err
		}
		if renewed {
			w, err = r.workspace(ctx, id)
			return w, token, false, err
		}
		r.revoke(id, now)
	}
	token = account.RandomToken()
	id := account.TokenHash(token)
	if err = r.db.CreateBrowserSession(ctx, id, account.RandomToken(), now.Add(Lifetime)); err != nil {
		return nil, "", false, err
	}
	w, err = r.workspace(ctx, id)
	return w, token, true, err
}

// Admit resolves a request to a workspace that accepted it; the caller must
// call Finish. A workspace can end between resolution and admission, so a
// browser session is resolved again once.
func (r *Registry) Admit(ctx context.Context, token string) (w *Workspace, kept string, fresh bool, err error) {
	if !r.cfg.Public {
		if !r.local.begin() {
			return nil, "", false, fault.ServerUnavailable
		}
		return r.local, "", false, nil
	}
	for range maxAdmissionAttempts {
		if w, kept, fresh, err = r.Resume(ctx, token); err != nil {
			return nil, "", false, err
		}
		if w.begin() {
			return w, kept, fresh, nil
		}
		token = kept
	}
	return nil, "", false, fault.ServerUnavailable
}

// workspace returns the running workspace of a session, starting it if needed.
func (r *Registry) workspace(ctx context.Context, id string) (*Workspace, error) {
	r.mu.Lock()
	w, full := r.workspaces[id], len(r.workspaces) >= maxActiveWorkspaces
	r.mu.Unlock()
	if w != nil {
		return w, nil
	}
	if err := r.ctx.Err(); err != nil {
		return nil, err
	}
	if full {
		return nil, errors.New("too many active browser sessions")
	}
	// Storage is read outside the registry lock; a concurrent start of the same session wins or loses whole.
	w, err := r.open(ctx, id)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing := r.workspaces[id]; existing != nil {
		w.cancel()
		return existing, nil
	}
	if r.ctx.Err() != nil {
		w.cancel()
		return nil, r.ctx.Err()
	}
	r.workspaces[id] = w
	w.wg.Go(w.work)
	return w, nil
}

func (r *Registry) open(ctx context.Context, id string) (*Workspace, error) {
	if err := r.db.EnsurePreferences(ctx, id); err != nil {
		return nil, err
	}
	saved, err := r.db.Workspace(ctx, id)
	if err != nil {
		return nil, err
	}
	w := &Workspace{r: r, Id: id, csrf: saved.Csrf, apple: newLock(), wake: make(chan struct{}, 1)}
	w.ctx, w.cancel = context.WithCancel(r.ctx)
	w.accountCtx, w.accountCancel = context.WithCancel(w.ctx)
	w.catalog.status = catalogIdle
	w.device = r.db.Device(saved.Device, id)
	if err = w.renewAuthenticator(); err != nil {
		w.cancel()
		return nil, err
	}
	record, err := r.db.ReadSession(ctx, id)
	switch {
	case err == nil:
		w.identity = &record.Identity
	case !fault.Is(err, fault.NotFound):
		w.cancel()
		return nil, err
	}
	return w, nil
}

func (r *Registry) all() []*Workspace {
	r.mu.Lock()
	defer r.mu.Unlock()
	all := make([]*Workspace, 0, len(r.workspaces))
	for _, w := range r.workspaces {
		all = append(all, w)
	}
	return all
}

// remove takes a workspace out of the registry if it is still the registered one.
func (r *Registry) remove(w *Workspace) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.workspaces[w.Id] != w {
		return false
	}
	delete(r.workspaces, w.Id)
	return true
}

// deviceLock serializes Apple operations of every workspace that shares a device.
func (r *Registry) deviceLock(id string) lock {
	if id == "" {
		return newLock()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	l := r.devices[id]
	if l == nil {
		l = newLock()
		r.devices[id] = l
	}
	return l
}

// deviceReplaced signs out every workspace that used a device whose identity changed.
func (r *Registry) deviceReplaced(deviceId string) {
	for _, w := range r.all() {
		w.deviceReplaced(deviceId)
	}
}

func (r *Registry) sweep() {
	ticker := time.NewTicker(sweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-r.ctx.Done():
			return
		case now := <-ticker.C:
			ids, err := r.db.ExpiredSessions(r.ctx, now)
			if err != nil {
				r.cfg.Report("sessions_expiry", err)
				continue
			}
			for _, id := range ids {
				r.revoke(id, now)
			}
			r.evictIdle(time.Now())
		}
	}
}

// evictIdle stops workspaces nobody uses. The browser session stays in the
// database; its next request starts a workspace again.
func (r *Registry) evictIdle(now time.Time) {
	for _, w := range r.all() {
		if w.evictIfIdle(now) {
			r.wg.Go(w.release)
		}
	}
}

// revoke ends a browser session unless it was renewed after the given moment.
func (r *Registry) revoke(id string, before time.Time) {
	revoked, err := r.db.RevokeSession(context.WithoutCancel(r.ctx), id, before)
	if err != nil || !revoked {
		r.cfg.Report("session_revoke", err)
		return
	}
	r.mu.Lock()
	w := r.workspaces[id]
	delete(r.workspaces, id)
	r.mu.Unlock()
	if w != nil {
		r.retire(w)
	}
}

// retire stops a workspace that left the registry and, once its requests and
// goroutines end, wipes what it kept in memory.
func (r *Registry) retire(w *Workspace) {
	w.stop()
	r.wg.Go(w.release)
}

// BeginShutdown cancels active work; requests already admitted may still finish.
func (r *Registry) BeginShutdown() { r.cancel() }

// Close stops every workspace and waits for their work to end; later calls do nothing.
func (r *Registry) Close() { r.closed.Do(r.close) }

func (r *Registry) close() {
	r.cancel()
	all := r.all()
	for _, w := range all {
		w.stop()
	}
	for _, w := range all {
		w.release()
	}
	r.wg.Wait()
	clear(r.localKey)
	r.rootPassword = ""
}

func (r *Registry) recoverTransfers() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.ctx.Done():
			return
		case <-ticker.C:
			err := r.db.FlushTransfers(r.ctx)
			r.library.StorageFailure(err)
			if err != nil {
				r.cfg.Report("download_recover", err)
				continue
			}
			if r.library.StorageError() != "" {
				continue
			}
			for _, w := range r.all() {
				if queued, err := r.db.Queued(r.ctx, w.Id); err == nil && queued {
					w.mu.Lock()
					if w.identity != nil && !w.login.required && w.leases > 0 {
						w.wakeWorker()
					}
					w.mu.Unlock()
				}
			}
		}
	}
}
