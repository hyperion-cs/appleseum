// Package app assembles the server: storage, the session registry, the
// library and the HTTP API, and runs their common life cycle.
package app

import (
	"cmp"
	"context"
	"errors"
	"io/fs"
	"log"
	"net"
	"net/http"
	"path/filepath"
	"sync"

	"github.com/gofrs/flock"
	"github.com/hyperion-cs/appleseum/applekit"
	appleartwork "github.com/hyperion-cs/appleseum/applekit/artwork"
	"github.com/hyperion-cs/appleseum/applekit/hosting"

	"appleseum/backend/internal/account"
	"appleseum/backend/internal/api"
	"appleseum/backend/internal/db"
	"appleseum/backend/internal/debuglog"
	"appleseum/backend/internal/library"
	"appleseum/backend/internal/product"
	"appleseum/backend/internal/rawlog"
	"appleseum/backend/internal/session"
)

type Config struct {
	DataDir string
	// DebugLogDir holds the debug journal; the default is logs inside DataDir.
	DebugLogDir   string
	Admin         bool
	Debug         bool
	Ish           bool
	Public        bool
	SharedLibrary bool
	// Web is the built interface; nil serves the API only.
	Web          fs.FS
	Hosts        []string
	Resolver     hosting.Resolver
	BonjourName  string
	HttpPort     string
	ManifestPort string

	// The rest replaces the outside world in tests.
	Apple          account.Apple
	Artwork        func(context.Context, string) ([]byte, error)
	ManifestLookup func(context.Context, string) ([]net.IPAddr, error)
}

// Validate rejects combinations of modes that cannot work together.
func (c Config) Validate() error {
	switch {
	case c.Ish && c.Public:
		return errors.New("--public is not supported in iSH")
	case c.Public && c.Debug:
		return errors.New("--public cannot be combined with --debug=web")
	case c.SharedLibrary && !c.Public:
		return errors.New("--shared-library requires --public")
	}
	return nil
}

type App struct {
	cfg      Config
	lock     *flock.Flock
	db       *db.Db
	debug    *debuglog.Service
	registry *session.Registry
	api      *api.Server
	cancel   context.CancelFunc

	closeOnce sync.Once
	closeErr  error
}

// New locks the data directory and starts everything but the listeners.
func New(cfg Config) (app *App, err error) {
	if err = cfg.Validate(); err != nil {
		return nil, err
	}
	a := &App{cfg: cfg}
	// Whatever was opened before a failure is closed again.
	defer func() {
		if err != nil {
			a.release()
		}
	}()
	a.lock = flock.New(filepath.Join(cfg.DataDir, ".lock"))
	if locked, err := a.lock.TryLock(); err != nil || !locked {
		a.lock = nil
		return nil, cmp.Or(err, errors.New("data directory is already in use"))
	}
	if a.db, err = db.Open(cfg.DataDir); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.cancel = cancel
	// Shares belong to one run; drop what an interrupted shutdown left.
	if err = a.db.ClearShares(ctx); err != nil {
		return nil, err
	}
	logs := cfg.DebugLogDir
	if logs == "" {
		logs = filepath.Join(cfg.DataDir, "logs")
	}
	if a.debug, err = debuglog.New(debuglog.Config{Enabled: cfg.Debug, Directory: logs, Web: cfg.Web}); err != nil {
		return nil, err
	}
	artwork := cfg.Artwork
	if artwork == nil {
		artwork = appleartwork.NewClient(nil).Fetch
	}
	lib := library.New(a.db, artwork, a.debug.SystemError, cfg.Public)
	limiter := new(api.Limiter)
	a.registry, err = session.New(a.trace(ctx), session.Config{
		Public: cfg.Public, SharedLibrary: cfg.SharedLibrary, Admin: cfg.Admin, Debug: cfg.Debug, Ish: cfg.Ish,
		Apple:      cfg.Apple,
		AllowLogin: func(login string) bool { return limiter.Allow(api.Login, "login:"+login) },
		Report:     a.debug.SystemError,
		Trace:      a.trace,
	}, a.db, lib)
	if err != nil {
		return nil, err
	}
	a.api = api.New(api.Config{
		Public: cfg.Public, Admin: cfg.Admin, Web: cfg.Web, Lookup: cfg.ManifestLookup, Report: a.debug.SystemError,
		Install: hosting.Config{
			Ish: cfg.Ish, Public: cfg.Public, LocalHosts: cfg.Hosts,
			Resolver: cfg.Resolver, BonjourName: cfg.BonjourName,
			HttpPort:     cmp.Or(cfg.HttpPort, product.DefaultHttpPort),
			ManifestPort: cmp.Or(cfg.ManifestPort, product.DefaultManifestPort),
		},
	}, a.registry, a.db, lib, a.debug, limiter)
	rawlog.Watch(ctx, func() {
		stats := a.db.Stats()
		rawlog.Log("sqlite open=%d in_use=%d idle=%d wait_count=%d wait_duration=%s max_open=%d", stats.OpenConnections, stats.InUse, stats.Idle, stats.WaitCount, stats.WaitDuration, stats.MaxOpenConnections)
	})
	return a, nil
}

// trace attaches diagnostics of Apple requests to a context: captured
// exchanges for the debug journal, or request lines for the raw log.
func (a *App) trace(ctx context.Context) context.Context {
	switch {
	case a.cfg.Debug:
		return a.debug.Context(ctx)
	case rawlog.Enabled():
		return rawlog.Trace(applekit.WithRequestMetadataObserver(ctx, a.debug.ObserveApple))
	}
	return ctx
}

func (a *App) Handler() http.Handler { return a.api.Handler() }

// Distribution serves installation manifests and shared files over HTTPS.
func (a *App) Distribution() http.Handler { return a.api.Distribution() }

// ServicePassword is the startup password of the service account in public mode.
func (a *App) ServicePassword() string { return a.registry.ServicePassword() }

func (a *App) DebugListener(server *http.Server, listener net.Listener) net.Listener {
	return a.debug.Listener(server, listener)
}

func (a *App) HttpErrorLog() *log.Logger { return a.debug.HttpErrorLog() }

// BeginShutdown refuses new requests and cancels background work; requests
// already accepted keep their storage until Close.
func (a *App) BeginShutdown() {
	a.api.BeginShutdown()
	a.registry.BeginShutdown()
}

// Close waits for accepted requests, stops every workspace and releases the data directory.
func (a *App) Close() error {
	a.closeOnce.Do(func() {
		a.BeginShutdown()
		a.api.Wait()
		a.closeErr = a.release()
	})
	return a.closeErr
}

func (a *App) release() error {
	var errs []error
	if a.registry != nil {
		a.registry.Close()
	}
	if a.cancel != nil {
		a.cancel()
	}
	if a.db != nil {
		errs = append(errs, a.db.ClearShares(context.Background()), a.db.Close())
	}
	if a.debug != nil {
		a.debug.Close()
	}
	if a.lock != nil {
		errs = append(errs, a.lock.Unlock())
	}
	return errors.Join(errs...)
}
