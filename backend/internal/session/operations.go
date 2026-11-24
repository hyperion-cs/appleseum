package session

import (
	"context"
	"errors"
	"time"

	"github.com/hyperion-cs/appleseum/applekit"

	"appleseum/backend/internal/account"
	"appleseum/backend/internal/catalog"
	"appleseum/backend/internal/db"
	"appleseum/backend/internal/fault"
	"appleseum/backend/internal/wire"
)

const (
	catalogRefreshTimeout = 15 * time.Minute
	versionRequestTimeout = 3 * time.Minute
	profileRequestTimeout = 2 * time.Minute
)

// signedIn returns the account and the context that ends when it leaves.
func (w *Workspace) signedIn() (db.Identity, context.Context, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.identity == nil {
		return db.Identity{}, nil, fault.SignInRequired
	}
	return *w.identity, w.accountCtx, nil
}

// bound limits a request to a duration and to the account's presence.
func bound(ctx, account context.Context, limit time.Duration) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(ctx, limit)
	stop := context.AfterFunc(account, cancel)
	return ctx, func() { stop(); cancel() }
}

// remote names a failed operation: Apple's failures by what the account needs, anything else as is.
func (w *Workspace) remote(ctx context.Context, err error) error {
	var remote *catalog.RemoteError
	if errors.As(err, &remote) {
		return w.appleFailure(ctx, remote.Err)
	}
	return err
}

// RefreshCatalog starts reading the purchase list from Apple; State reports its progress.
func (w *Workspace) RefreshCatalog() error {
	w.mu.Lock()
	switch {
	case w.identity == nil:
		w.mu.Unlock()
		return fault.SignInRequired
	case w.catalog.status == wire.CatalogLoading:
		w.mu.Unlock()
		return fault.Busy
	case !w.retainSecrets():
		w.mu.Unlock()
		return fault.SessionKeyRequired
	}
	owner, epoch := w.identity.Owner, w.accountCtx
	w.catalog = catalogState{status: wire.CatalogLoading}
	w.mu.Unlock()

	current := func() bool { return w.accountCtx == epoch && epoch.Err() == nil }
	w.wg.Go(func() {
		defer w.releaseSecrets()
		ctx, cancel := context.WithTimeout(epoch, catalogRefreshTimeout)
		defer cancel()
		var code fault.Code
		if err := w.apple.Lock(ctx); err == nil {
			err = catalog.Refresh(ctx, w.r.db, w.r.library.Icons, w.call, db.Scope{Owner: owner, Public: w.r.cfg.Public, SharedLibrary: w.r.cfg.SharedLibrary}, w.Id, func(value float64) {
				w.mu.Lock()
				defer w.mu.Unlock()
				if current() && w.catalog.status == wire.CatalogLoading && value > w.catalog.progress {
					w.catalog.progress = min(value, 99)
				}
			})
			if err = w.remote(ctx, err); err != nil {
				if code = fault.Of(err); code == fault.StorageFailed {
					w.r.cfg.Report("catalog_save", err)
				}
			}
			w.apple.Unlock()
		} else {
			code = fault.AppleUnavailable
		}
		w.mu.Lock()
		defer w.mu.Unlock()
		if !current() {
			return
		}
		if code == "" {
			w.catalog = catalogState{status: wire.CatalogReady, progress: 100}
		} else {
			w.catalog.status, w.catalog.err = wire.CatalogFailed, code
		}
	})
	return nil
}

// ClearCatalog forgets the saved purchase list.
func (w *Workspace) ClearCatalog(ctx context.Context) error {
	identity, _, err := w.signedIn()
	if err != nil {
		return err
	}
	// A refresh holds this lock while it writes the list.
	if !w.apple.TryLock() {
		return fault.Busy
	}
	defer w.apple.Unlock()
	w.mu.Lock()
	loading := w.catalog.status == wire.CatalogLoading
	w.mu.Unlock()
	if loading {
		return fault.Busy
	}
	if err = w.r.library.Icons.ClearCatalog(ctx, identity.Owner); err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.catalog.status != wire.CatalogLoading {
		w.catalog = catalogState{status: catalogIdle}
	}
	return nil
}

// Versions reads a page of an app's versions, reporting percent progress while Apple describes them.
func (w *Workspace) Versions(ctx context.Context, appId int64, input wire.VersionPageRequest, progress func(int)) (wire.VersionPage, error) {
	identity, accountCtx, err := w.signedIn()
	if err != nil {
		return wire.VersionPage{}, err
	}
	ctx, cancel := bound(w.r.cfg.Trace(ctx), accountCtx, versionRequestTimeout)
	defer cancel()
	progress(0)
	if err = w.apple.Lock(ctx); err != nil {
		return wire.VersionPage{}, err
	}
	defer w.apple.Unlock()
	page, err := catalog.Versions(ctx, w.r.db, w.call, identity.Owner, appId, input, progress)
	return page, w.remote(ctx, err)
}

// Queue adds the selected apps to the download queue and starts the worker.
func (w *Workspace) Queue(ctx context.Context, request wire.DownloadRequest) error {
	identity, _, err := w.signedIn()
	if err != nil {
		return err
	}
	scope := w.Actor().Scope()
	scope.Owner = identity.Owner
	if err = w.r.library.Queue(ctx, scope, w.Id, request); err != nil {
		return err
	}
	w.mu.Lock()
	w.wakeWorker()
	w.mu.Unlock()
	return nil
}

// Profile returns the account summary; refresh asks Apple for the profile again.
func (w *Workspace) Profile(ctx context.Context, refresh bool) (wire.ProfileSummary, error) {
	identity, accountCtx, err := w.signedIn()
	if err != nil {
		return wire.ProfileSummary{}, err
	}
	ctx, cancel := bound(w.r.cfg.Trace(ctx), accountCtx, profileRequestTimeout)
	defer cancel()
	var result wire.ProfileSummary
	if refresh {
		if err = w.apple.Lock(ctx); err != nil {
			return result, err
		}
		result, err = account.RefreshProfile(ctx, w.r.db, identity.Owner, func(ctx context.Context) (profile applekit.AccountProfile, err error) {
			err = w.call(ctx, func(session account.Session) error { profile, err = session.Profile(ctx); return err })
			return profile, err
		})
		w.apple.Unlock()
	} else {
		result, err = account.Profile(ctx, w.r.db, identity.Owner)
	}
	// The account may have left or renewed its session while Apple answered.
	now, _, gone := w.signedIn()
	switch {
	case accountCtx.Err() != nil || gone != nil || now.Owner != identity.Owner:
		return result, fault.SignInRequired
	case err != nil:
		return result, err
	}
	result.SignedInAt = account.SignedInAt(now)
	return result, nil
}

// ClearProfile forgets the cached Apple profile.
func (w *Workspace) ClearProfile(ctx context.Context) error {
	identity, accountCtx, err := w.signedIn()
	if err != nil {
		return err
	}
	if err = w.apple.Lock(ctx); err != nil {
		return err
	}
	defer w.apple.Unlock()
	if accountCtx.Err() != nil {
		return fault.SignInRequired
	}
	return w.r.db.ClearProfile(ctx, identity.Owner)
}
