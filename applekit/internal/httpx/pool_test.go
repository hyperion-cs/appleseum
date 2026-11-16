package httpx

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Trust only the local test server, retaining the real profiles and TLS bridge.
func localClient(t testing.TB, server *httptest.Server) *http.Client {
	t.Helper()
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	client := NewClientWithOptions(nil, ClientOptions{RootCas: roots})
	client.Timeout = 5 * time.Second
	t.Cleanup(client.CloseIdleConnections)
	return client
}

func localServer(t testing.TB, h2 bool, handler http.HandlerFunc) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	connections := &atomic.Int32{}
	server := httptest.NewUnstartedServer(handler)
	server.EnableHTTP2 = h2
	server.Config.ErrorLog = log.New(io.Discard, "", 0)

	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}

	server.StartTLS()
	t.Cleanup(server.Close)
	return server, connections
}

func poolRequest(t testing.TB, client *http.Client, ctx context.Context, url string, profile Profile) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(WithProfile(ctx, profile), http.MethodPost, url, strings.NewReader("synthetic-body"))
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(io.Discard, response.Body); err != nil {
		response.Body.Close()
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK || response.TLS == nil || !response.TLS.HandshakeComplete || len(response.TLS.VerifiedChains) == 0 {
		t.Fatal("missing successful response or verified TLS metadata")
	}
	return response
}

func TestConnectionReuseAndIsolation(t *testing.T) {
	for _, h2 := range []bool{false, true} {
		t.Run(fmt.Sprintf("HTTP2=%v", h2), func(t *testing.T) {
			var posts atomic.Int32
			server, connections := localServer(t, h2, func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				if string(body) != "synthetic-body" || r.Header.Get("User-Agent") == "" || r.Header.Get("Accept-Encoding") != "" {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				posts.Add(1)
				if cookie, err := r.Cookie("ticket"); err == nil {
					w.Header().Set("X-Received-Cookie", cookie.Value)
				}
				http.SetCookie(w, &http.Cookie{Name: "ticket", Value: "rotated", Path: "/", Secure: true})
				w.Header().Set("X-Connection", r.RemoteAddr)
				io.WriteString(w, "synthetic-response")
			})
			client := localClient(t, server)
			ctx, cancel := context.WithCancel(context.Background())
			first := poolRequest(t, client, ctx, server.URL, Accounts)
			cancel()
			for range 3 {
				response := poolRequest(t, client, context.Background(), server.URL, Accounts)
				if response.Header.Get("X-Connection") != first.Header.Get("X-Connection") || response.Header.Get("X-Received-Cookie") != "rotated" {
					t.Fatal("connection or cookies lost between requests")
				}
			}
			expectedProtocol := 1
			if h2 {
				expectedProtocol = 2
			}
			if first.ProtoMajor != expectedProtocol || connections.Load() != 1 {
				t.Fatal("ALPN dispatch or reuse failed", first.Proto, connections.Load())
			}
			// These profiles share a ClientHello but require distinct pools.
			other := poolRequest(t, client, context.Background(), server.URL, NativeAuthentication)
			if other.Header.Get("X-Connection") == first.Header.Get("X-Connection") || connections.Load() != 2 {
				t.Fatal("TLS profiles shared a connection")
			}
			poolRequest(t, client, context.Background(), server.URL, Accounts)
			if connections.Load() != 2 {
				t.Fatal("switching profiles discarded the original connection")
			}
			independent := localClient(t, server)
			poolRequest(t, independent, context.Background(), server.URL, Accounts)
			if connections.Load() != 3 {
				t.Fatal("separate clients shared a pool")
			}
			client.CloseIdleConnections()
			poolRequest(t, client, context.Background(), server.URL, Accounts)
			if connections.Load() != 4 || posts.Load() != 8 {
				t.Fatal("idle cleanup or request count differs", connections.Load(), posts.Load())
			}
			second, secondConnections := localServer(t, h2, func(w http.ResponseWriter, r *http.Request) {
				io.WriteString(w, "another origin")
			})
			poolRequest(t, client, context.Background(), second.URL, Accounts)
			poolRequest(t, client, context.Background(), second.URL, Accounts)
			poolRequest(t, client, context.Background(), server.URL, Accounts)
			if secondConnections.Load() != 1 || connections.Load() != 4 {
				t.Fatal("origin pools were mixed or discarded")
			}
		})
	}
}

func TestHttp2CancelOnlyItsStream(t *testing.T) {
	server, connections := localServer(t, true, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/blocked" {
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			return
		}
		io.WriteString(w, "complete")
	})
	client := localClient(t, server)
	poolRequest(t, client, context.Background(), server.URL, Configurator)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/blocked", nil)
	blocked, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer blocked.Body.Close()
	poolRequest(t, client, context.Background(), server.URL, Configurator)
	if connections.Load() != 1 {
		t.Fatal("concurrent HTTP/2 streams did not share the connection")
	}
	cancel()
	_, err = io.ReadAll(blocked.Body)
	if !errors.Is(err, context.Canceled) {
		t.Fatal("stream cancellation was lost", err)
	}
	blocked.Body.Close()
	poolRequest(t, client, context.Background(), server.URL, Configurator)
	if connections.Load() != 1 {
		t.Fatal("canceling one stream closed the HTTP/2 connection")
	}
}

func TestPoolDoesNotReplayProcessedPost(t *testing.T) {
	for _, h2 := range []bool{false, true} {
		t.Run(fmt.Sprintf("HTTP2=%v", h2), func(t *testing.T) {
			var posts atomic.Int32
			server, _ := localServer(t, h2, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/drop" {
					io.Copy(io.Discard, r.Body)
					posts.Add(1)
					panic(http.ErrAbortHandler)
				}
				io.WriteString(w, "warm")
			})
			client := localClient(t, server)
			poolRequest(t, client, context.Background(), server.URL, Configurator)
			req, _ := http.NewRequest(http.MethodPost, server.URL+"/drop", strings.NewReader("synthetic-sensitive-request"))
			response, err := client.Do(req)
			if response != nil {
				response.Body.Close()
			}
			if err == nil || posts.Load() != 1 {
				t.Fatal("failed processed POST was replayed", err, posts.Load())
			}
			poolRequest(t, client, context.Background(), server.URL, Configurator)
		})
	}
}

func TestProductionDialRejectsUntrustedCertificate(t *testing.T) {
	server, _ := localServer(t, true, func(w http.ResponseWriter, r *http.Request) {
		t.Error("untrusted server received an HTTP request")
	})
	client := NewClient(nil)
	defer client.CloseIdleConnections()
	_, err := client.Get(server.URL)
	var unknown x509.UnknownAuthorityError
	if !errors.As(err, &unknown) {
		t.Fatal("production dial accepted an untrusted certificate", err)
	}
}

func TestEveryProfileRetainsItsAlpnAndUserAgent(t *testing.T) {
	server, connections := localServer(t, true, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-User-Agent", r.UserAgent())
		io.WriteString(w, "response")
	})
	client := localClient(t, server)
	for profile := Configurator; profile <= AmsAccounts; profile++ {
		for range 2 {
			response := poolRequest(t, client, context.Background(), server.URL, profile)
			protocol := 2
			if profile == AuthKitPostData {
				protocol = 1
			}
			if response.ProtoMajor != protocol || response.Header.Get("X-User-Agent") != profile.UserAgent() {
				t.Fatal("profile's ALPN or User-Agent changed", profile, response.Proto)
			}
		}
	}
	if connections.Load() != int32(AmsAccounts+1) {
		t.Fatal("profiles did not each reuse their own connection", connections.Load())
	}
}

func TestClosingUnreadResponseKeepsPoolUsable(t *testing.T) {
	for _, h2 := range []bool{false, true} {
		t.Run(fmt.Sprintf("HTTP2=%v", h2), func(t *testing.T) {
			server, connections := localServer(t, h2, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/unread" {
					w.Header().Set("Content-Length", "65536")
					io.WriteString(w, strings.Repeat("x", 65_536))
					return
				}
				w.Header().Set("X-Complete", "true")
				io.WriteString(w, "response")
			})
			client := localClient(t, server)
			response, err := client.Get(server.URL + "/unread")
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			complete := poolRequest(t, client, context.Background(), server.URL, Configurator)
			count := connections.Load()
			// HTTP/1 may drain a small unread body or open a new connection.
			if complete.Header.Get("X-Complete") != "true" || count < 1 || count > 2 || (h2 && count != 1) {
				t.Fatal("unread response left the connection in an invalid state", connections.Load())
			}
		})
	}
}

func BenchmarkConnectionReuse(b *testing.B) {
	for _, h2 := range []bool{false, true} {
		for _, reuse := range []bool{true, false} {
			b.Run(fmt.Sprintf("HTTP2=%v/reuse=%v", h2, reuse), func(b *testing.B) {
				server, connections := localServer(b, h2, func(w http.ResponseWriter, r *http.Request) {
					io.Copy(io.Discard, r.Body)
					io.WriteString(w, "response")
				})
				client := localClient(b, server)
				b.ReportAllocs()
				b.ResetTimer()
				for range b.N {
					poolRequest(b, client, context.Background(), server.URL, Configurator)
					if !reuse {
						client.CloseIdleConnections()
					}
				}
				b.StopTimer()
				b.ReportMetric(float64(connections.Load()), "connections")
			})
		}
	}
}
