// Package api is the HTTP face of the server: it admits requests, checks who
// makes them and translates between JSON and the operations of the packages below.
package api

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/hyperion-cs/appleseum/applekit/hosting"

	"appleseum/backend/internal/account"
	"appleseum/backend/internal/db"
	"appleseum/backend/internal/debuglog"
	"appleseum/backend/internal/fault"
	"appleseum/backend/internal/library"
	"appleseum/backend/internal/session"
	"appleseum/backend/internal/sharing"
)

const (
	contentSecurityPolicy = "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'"
	sessionKeyHeader      = "X-Session-Key"
	csrfHeader            = "X-CSRF-Token"
	sessionResetHeader    = "X-Appleseum-Session-Reset"
)

type Config struct {
	Public, Admin bool
	// Install describes the addresses installation links may use.
	Install hosting.Config
	// Web holds the built interface; nil serves the API only.
	Web fs.FS
	// Lookup resolves a manifest host before sharing; nil uses the system resolver.
	Lookup func(context.Context, string) ([]net.IPAddr, error)
	Report func(operation string, err error)
}

// Server answers the JSON API and serves the interface and shared files.
type Server struct {
	cfg      Config
	registry *session.Registry
	db       *db.Db
	library  *library.Service
	debug    *debuglog.Service
	limiter  *Limiter
	mux      *http.ServeMux
	routes   []route
	progress sharing.Progress

	// Requests in flight; Wait returns after the last one that was accepted.
	mu       sync.Mutex
	stopping bool
	flights  sync.WaitGroup
}

func New(cfg Config, registry *session.Registry, s *db.Db, lib *library.Service, debug *debuglog.Service, limiter *Limiter) *Server {
	a := &Server{cfg: cfg, registry: registry, db: s, library: lib, debug: debug, limiter: limiter, mux: http.NewServeMux()}
	a.routes = a.table()
	for _, rt := range a.routes {
		a.mux.HandleFunc(rt.pattern, func(w http.ResponseWriter, r *http.Request) { a.serve(w, r, rt) })
	}
	a.mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) { a.fail(w, fault.NotFound) })
	if cfg.Web != nil {
		a.mux.Handle("/", a.static())
	}
	return a
}

func (a *Server) report(operation string, err error) { a.cfg.Report(operation, err) }

// Handler serves the interface, the API and, in public mode, shared files.
func (a *Server) Handler() http.Handler {
	front := a.debug.Trace("API", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.allowed(r.Host, false) {
			a.fail(w, fault.InvalidRequest)
			return
		}
		header := w.Header()
		header.Set("X-Content-Type-Options", "nosniff")
		header.Set("Referrer-Policy", "no-referrer")
		header.Set("X-Frame-Options", "DENY")
		header.Set("Content-Security-Policy", contentSecurityPolicy)
		a.mux.ServeHTTP(w, r)
	}))
	packages := a.delivery()
	return a.tracked(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/packages/") || a.cfg.Public && strings.HasPrefix(r.URL.Path, "/manifests/") {
			// A large IPA transfer may outlive the API's write timeout.
			http.NewResponseController(w).SetWriteDeadline(time.Time{})
			packages.ServeHTTP(w, r)
			return
		}
		front.ServeHTTP(w, r)
	}))
}

// Distribution serves installation manifests and shared files on the HTTPS listener.
func (a *Server) Distribution() http.Handler { return a.tracked(a.delivery()) }

// BeginShutdown refuses new requests; Wait returns when accepted ones finished.
func (a *Server) BeginShutdown() {
	a.mu.Lock()
	a.stopping = true
	a.mu.Unlock()
}

func (a *Server) Wait() { a.flights.Wait() }

func (a *Server) tracked(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		if a.stopping {
			a.mu.Unlock()
			a.fail(w, fault.ServerUnavailable)
			return
		}
		a.flights.Add(1)
		a.mu.Unlock()
		defer a.flights.Done()
		handler.ServeHTTP(w, r)
	})
}

// visit is one admitted request: its workspace and who makes it.
type visit struct {
	space *session.Workspace
	actor session.Actor
}

// refusal is a transport-level rejection whose status differs from its code's usual one.
type refusal struct {
	status int
	code   fault.Code
}

func (e refusal) Error() string { return string(e.code) }

func (e refusal) Unwrap() error { return e.code }

var (
	errForbidden = refusal{http.StatusForbidden, fault.InvalidRequest}
	errMediaType = refusal{http.StatusUnsupportedMediaType, fault.InvalidRequest}
	errMethod    = refusal{http.StatusMethodNotAllowed, fault.InvalidRequest}
)

// serve runs a route for one request: admission to a workspace, the session
// key, the rate limit, the CSRF check, access, and only then the handler.
func (a *Server) serve(w http.ResponseWriter, r *http.Request, rt route) {
	space, err := a.admit(w, r)
	if err != nil {
		a.fail(w, err)
		return
	}
	defer space.Finish()
	if rt.key != noKey {
		release, err := a.unlock(space, r)
		if err != nil {
			a.fail(w, err)
			return
		}
		defer release()
	}
	v := visit{space: space, actor: space.Actor()}
	if err = a.check(r, rt, v); err == nil {
		err = rt.handle(w, r, v)
	}
	// Nobody reads the answer to a request that has gone away.
	if err != nil && r.Context().Err() == nil {
		a.fail(w, err)
	}
}

func (a *Server) admit(w http.ResponseWriter, r *http.Request) (*session.Workspace, error) {
	token := ""
	if cookie, err := r.Cookie(SessionCookie); a.cfg.Public && err == nil {
		token = cookie.Value
	}
	space, kept, fresh, err := a.registry.Admit(r.Context(), token)
	if err != nil {
		return nil, err
	}
	if a.cfg.Public {
		SetSessionCookie(w, kept, time.Now(), session.Lifetime)
		if fresh {
			// The browser's saved session key belongs to the session that ended.
			w.Header().Set(sessionResetHeader, "1")
		}
	}
	return space, nil
}

// unlock leases the saved Apple session to the request, with the browser's key in public mode.
func (a *Server) unlock(space *session.Workspace, r *http.Request) (func(), error) {
	var key []byte
	if value := r.Header.Get(sessionKeyHeader); a.cfg.Public && value != "" {
		var err error
		if key, err = base64.RawURLEncoding.DecodeString(value); err != nil || len(key) != account.KeySize {
			return nil, fault.InvalidRequest
		}
		defer clear(key)
	}
	release, err := space.Acquire(r.Context(), key)
	if a.cfg.Public && errors.Is(err, account.ErrKey) {
		return nil, fault.SessionKeyRequired
	}
	return release, err
}

func (a *Server) check(r *http.Request, rt route, v visit) error {
	if !a.limiter.Allow(rt.class, rateIdentity(r, rt.class, v.actor)) {
		return fault.RateLimited
	}
	if rt.access == debugging && !a.debug.Enabled() {
		return fault.NotFound
	}
	// The journal holds credentials in transit, so even reading it needs the token.
	if r.Method != http.MethodGet && r.Method != http.MethodHead || rt.access == debugging {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get(csrfHeader)), []byte(v.space.Csrf())) != 1 {
			return errForbidden
		}
		if origin := r.Header.Get("Origin"); origin != "" && origin != a.origin(r) {
			return errForbidden
		}
	}
	if rt.key == keyRequired && a.cfg.Public && !v.actor.Service && !v.space.Unlocked() {
		return fault.SessionKeyRequired
	}
	return rt.access.allows(v.actor)
}

// rateIdentity counts a signed-in account as one client wherever it connects
// from; sign-in attempts and file transfers are counted per address.
func rateIdentity(r *http.Request, class RateClass, actor session.Actor) string {
	if actor.SignedIn && class != Login && class != LoginPeer && class != Files {
		return "account:" + actor.Owner
	}
	return addressIdentity(r)
}

func addressIdentity(r *http.Request) string {
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	return "ip:" + ip
}

func (a *Server) scheme(r *http.Request) string {
	if a.cfg.Public || r.TLS != nil {
		return "https"
	}
	return "http"
}

func (a *Server) origin(r *http.Request) string {
	return (&url.URL{Scheme: a.scheme(r), Host: r.Host}).String()
}
