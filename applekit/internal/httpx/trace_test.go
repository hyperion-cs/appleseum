package httpx

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"sync/atomic"
	"testing"
	"time"
)

func TestCustomTlsReportsHandshakeTraceAndReusesConnection(t *testing.T) {
	server, _ := localServer(t, true, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok") })
	client := localClient(t, server)
	var starts, finishes atomic.Int32
	ctx := httptrace.WithClientTrace(context.Background(), &httptrace.ClientTrace{
		TLSHandshakeStart: func() { starts.Add(1) },
		TLSHandshakeDone: func(state tls.ConnectionState, err error) {
			if err != nil || !state.HandshakeComplete {
				t.Errorf("handshake state=%v error=%v", state.HandshakeComplete, err)
			}
			finishes.Add(1)
		},
	})
	for range 2 {
		poolRequest(t, client, ctx, server.URL, Configurator)
	}
	if starts.Load() != 1 || finishes.Load() != 1 {
		t.Fatalf("trace called %d starts and %d finishes", starts.Load(), finishes.Load())
	}
}

func TestCustomTlsTraceCoversTheActualHandshakeAndFailures(t *testing.T) {
	for _, reject := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failure"}[reject], func(t *testing.T) {
			var starts, finishes atomic.Int32
			var sawStart, sawFailure atomic.Bool
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "ok") }))
			server.Config.ErrorLog = log.New(io.Discard, "", 0)
			server.TLS = &tls.Config{GetConfigForClient: func(*tls.ClientHelloInfo) (*tls.Config, error) {
				sawStart.Store(starts.Load() == 1)
				if reject {
					return nil, errors.New("synthetic handshake rejection")
				}
				return nil, nil
			}}
			server.StartTLS()
			defer server.Close()
			client := localClient(t, server)
			ctx := httptrace.WithClientTrace(context.Background(), &httptrace.ClientTrace{
				TLSHandshakeStart: func() { starts.Add(1) },
				TLSHandshakeDone: func(_ tls.ConnectionState, err error) {
					sawFailure.Store(err != nil)
					finishes.Add(1)
				},
			})
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
			resp, err := client.Do(req)
			if resp != nil {
				io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
			}
			if (err != nil) != reject || !sawStart.Load() || starts.Load() != 1 || finishes.Load() != 1 || sawFailure.Load() != reject {
				t.Fatalf("error=%v started_before_hello=%t starts=%d finishes=%d failure_reported=%t", err, sawStart.Load(), starts.Load(), finishes.Load(), sawFailure.Load())
			}
		})
	}
}

func TestDelegatedTlsHandshakeKeepsDialDeadline(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()
	done := make(chan struct{})
	go func() { io.Copy(io.Discard, server); close(done) }()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	conn, err := Configurator.dialTls(ctx, "tcp", "example.com:443", ClientOptions{
		DialContext: func(context.Context, string, string) (net.Conn, error) { return client, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	// A non-responding peer must still hit the deadline established by dialing,
	// even though net/http calls HandshakeContext with a different context.
	result := make(chan error, 1)
	go func() { result <- conn.(*tlsConn).HandshakeContext(context.Background()) }()
	select {
	case err := <-result:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("handshake error=%v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("handshake lost its dial deadline")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out handshake left the socket open")
	}
}
