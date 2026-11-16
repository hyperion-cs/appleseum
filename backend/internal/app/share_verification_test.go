package app

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hyperion-cs/appleseum/applekit/hosting"

	"appleseum/backend/internal/rawlog"
	"appleseum/backend/internal/wire"
)

type verification struct {
	app   *App
	dir   string
	token string
	mu    sync.Mutex
	look  func(context.Context, string) ([]net.IPAddr, error)
}

func (v *verification) lookup(ctx context.Context, host string) ([]net.IPAddr, error) {
	v.mu.Lock()
	look := v.look
	v.mu.Unlock()
	return look(ctx, host)
}

func (v *verification) setLookup(look func(context.Context, string) ([]net.IPAddr, error)) {
	v.mu.Lock()
	v.look = look
	v.mu.Unlock()
}

// verificationServer runs in iSH, where a share created on the installing
// device needs its manifest host to resolve to the device itself.
func verificationServer(t *testing.T, debug bool) *verification {
	t.Helper()
	v := &verification{dir: dataDir(t)}
	hosts := (hosting.Config{Resolver: hosting.Nip, Ish: true, LocalHosts: []string{"127.0.0.1", "192.168.50.10"}}).Hosts()
	a, err := New(Config{Resolver: hosting.Nip, DataDir: v.dir, Ish: true, Hosts: hosts, Debug: debug, ManifestLookup: v.lookup})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := a.Close(); err != nil {
			t.Error(err)
		}
	})
	v.app = a
	db := database(t, v.dir)
	seedShares(t, db, v.dir)
	if _, err = db.Exec("DELETE FROM shares"); err != nil {
		t.Fatal(err)
	}
	v.token = csrf(t, a)
	return v
}

func (v *verification) request(method, path, peer string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://127.0.0.1:8080"+path, strings.NewReader(`{"ids":["`+lifecyclePackage+`"]}`))
	r.RemoteAddr = net.JoinHostPort(peer, "10000")
	r = r.WithContext(context.WithValue(r.Context(), http.LocalAddrContextKey, &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 8_080}))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-CSRF-Token", v.token)
	out := httptest.NewRecorder()
	v.app.Handler().ServeHTTP(out, r)
	return out
}

func TestShareDnsFailureBlocksCreationAndReportsDiagnostics(t *testing.T) {
	for _, mode := range []string{"web", "raw"} {
		t.Run(mode, func(t *testing.T) {
			var path string
			if mode == "raw" {
				path = filepath.Join(t.TempDir(), "debug.log")
				stop, err := rawlog.Start(path)
				if err != nil {
					t.Fatal(err)
				}
				defer stop()
			}
			v := verificationServer(t, mode == "web")
			db := database(t, v.dir)
			for _, test := range []struct {
				reason string
				ips    []net.IPAddr
				err    error
			}{
				{"unexpected_ip", []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}, {IP: net.ParseIP("2001:db8::9")}}, nil},
				{"resolve_timeout", nil, &net.DNSError{Err: "i/o timeout", IsTimeout: true}},
				{"resolve_failed", nil, &net.DNSError{Err: "no such host", IsNotFound: true}},
				{"empty_answer", nil, nil},
			} {
				v.setLookup(func(_ context.Context, host string) ([]net.IPAddr, error) {
					if host != "7f000001.nip.io." {
						t.Error("wrong DNS name", host)
					}
					return test.ips, test.err
				})
				out := v.request("POST", "/api/shares", "127.0.0.1")
				if out.Code != 502 || !strings.Contains(out.Body.String(), `"code":"delivery_verification_failed"`) || strings.Contains(out.Body.String(), "nip.io") {
					t.Fatalf("wrong user error: %d %s", out.Code, out.Body.String())
				}
				checkStorage(t, db, v.dir, 0)
				var diagnostics string
				if mode == "web" {
					out = v.request("GET", "/api/debug?source=Hosting&q=manifest_dns&limit=100", "127.0.0.1")
					var page wire.DebugPage
					if err := json.Unmarshal(out.Body.Bytes(), &page); err != nil || len(page.Items) == 0 {
						t.Fatal("missing web DNS diagnostic", err, out.Body.String())
					}
					diagnostics = page.Items[0].Detail
				} else {
					raw, err := os.ReadFile(path)
					if err != nil {
						t.Fatal(err)
					}
					diagnostics = string(raw)
				}
				for _, want := range []string{test.reason, "7f000001.nip.io", "expected=127.0.0.1"} {
					if !strings.Contains(diagnostics, want) {
						t.Fatalf("missing %q in DNS diagnostic: %s", want, diagnostics)
					}
				}
				if test.reason == "unexpected_ip" && !strings.Contains(diagnostics, "2001:db8::9") {
					t.Fatal("foreign IP missing from debug", diagnostics)
				}
			}
		})
	}
}

func TestShareDnsCheckRunsOnlyOnSameDeviceCreation(t *testing.T) {
	v := verificationServer(t, true)
	calls := 0
	v.setLookup(func(context.Context, string) ([]net.IPAddr, error) {
		calls++
		return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
	})
	out := v.request("POST", "/api/shares", "127.0.0.1")
	var share wire.Share
	if err := json.Unmarshal(out.Body.Bytes(), &share); err != nil || out.Code != 201 || !strings.Contains(share.Url, "7f000001.nip.io") || calls != 1 {
		t.Fatalf("share was not verified: status=%d calls=%d share=%+v err=%v", out.Code, calls, share, err)
	}
	for range 3 {
		out = v.request("GET", "/api/state", "127.0.0.1")
		var state wire.State
		if err := json.Unmarshal(out.Body.Bytes(), &state); err != nil || state.ShareCount != 1 {
			t.Fatal("polling lost the share link", err, out.Body.String())
		}
	}
	out = v.request("GET", "/api/shares/"+share.Id, "127.0.0.1")
	var current wire.Share
	if err := json.Unmarshal(out.Body.Bytes(), &current); err != nil || current.Url != share.Url {
		t.Fatal("share detail lost link")
	}
	if calls != 1 {
		t.Fatal("background polling repeated the DNS check", calls)
	}
	if _, err := database(t, v.dir).Exec("DELETE FROM shares"); err != nil {
		t.Fatal(err)
	}
	v.setLookup(func(context.Context, string) ([]net.IPAddr, error) {
		t.Error("two-device sharing must not depend on alias DNS")
		return nil, nil
	})
	if out = v.request("POST", "/api/shares", "192.168.50.11"); out.Code != 201 || strings.Contains(out.Body.String(), "nip.io") {
		t.Fatal("two-device sharing failed", out.Code, out.Body.String())
	}
}

func TestShareDnsLookupDoesNotBlockState(t *testing.T) {
	v := verificationServer(t, false)
	entered, release := make(chan struct{}), make(chan struct{})
	v.setLookup(func(ctx context.Context, _ string) ([]net.IPAddr, error) {
		close(entered)
		select {
		case <-release:
			return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- v.request("POST", "/api/shares", "127.0.0.1") }()
	<-entered
	state := make(chan *httptest.ResponseRecorder, 1)
	go func() { state <- v.request("GET", "/api/state", "127.0.0.1") }()
	select {
	case out := <-state:
		if out.Code != 200 {
			t.Error("state failed during DNS lookup", out.Code)
		}
	case <-time.After(time.Second):
		t.Error("DNS lookup blocked the workspace")
	}
	close(release)
	if out := <-done; out.Code != 201 {
		t.Fatal("share failed after DNS check", out.Code, out.Body.String())
	}
}
