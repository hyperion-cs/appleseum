package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestStartupFailureHintIsOnlyForIsh(t *testing.T) {
	if strings.Contains(startupFailureMessage(false), "airplane") {
		t.Fatal("ordinary platforms received an iSH-specific hint")
	}
	if !strings.Contains(startupFailureMessage(true), "airplane mode") {
		t.Fatal("iSH timeout is missing its network hint")
	}
}

func TestStartupDeadlineExpires(t *testing.T) {
	expired := make(chan struct{})
	ctx, finish := startupDeadline(20*time.Millisecond, func() { close(expired) })
	defer finish()
	select {
	case <-expired:
	case <-time.After(time.Second):
		t.Fatal("startup deadline did not fire")
	}
	if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatal(ctx.Err())
	}
}

func TestStartupDeadlineStopsWhenReady(t *testing.T) {
	expired := make(chan struct{})
	ctx, finish := startupDeadline(20*time.Millisecond, func() { close(expired) })
	finish()
	finish()
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatal(ctx.Err())
	}
	select {
	case <-expired:
		t.Fatal("startup timer fired after readiness")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestStartupDeadlineFinishRace(t *testing.T) {
	for range 50 {
		var calls atomic.Int32
		_, finish := startupDeadline(time.Nanosecond, func() { calls.Add(1) })
		finish()
		finish()
		if calls.Load() > 1 {
			t.Fatal("timeout callback ran more than once")
		}
	}
}

func TestStartupProbeUsesLocalListenerWithoutProxy(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	for _, tc := range []struct{ network, address, host string }{
		{"tcp4", "127.0.0.1:0", "127.0.0.1"},
		{"tcp", "0.0.0.0:0", "127.0.0.1"},
		{"tcp", ":0", "127.0.0.1"},
		{"tcp6", "[::]:0", "::1"},
	} {
		t.Run(tc.address, func(t *testing.T) {
			listener, err := net.Listen(tc.network, tc.address)
			if err != nil {
				if tc.network == "tcp6" {
					t.Skip(err)
				}
				t.Fatal(err)
			}
			var requests atomic.Int32
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.URL.Path != "/api/health" {
					t.Errorf("unexpected probe: %s", r.URL)
				}
				host, _, _ := net.SplitHostPort(r.Host)
				if host != tc.host {
					t.Errorf("probe host = %s, want %s", host, tc.host)
				}
				w.WriteHeader(http.StatusOK)
			}))
			server.Listener.Close()
			server.Listener = listener
			server.Start()
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := waitForStartup(ctx, tc.address, listener.Addr(), make(chan error)); err != nil {
				t.Fatal(err)
			}
			if requests.Load() != 1 {
				t.Fatal("local health request did not arrive")
			}
		})
	}
}

func TestStartupProbeTimesOut(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := waitForStartup(ctx, server.Listener.Addr().String(), server.Listener.Addr(), make(chan error)); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("unexpected timeout: %v", err)
	}
}

func TestStartupProbePreservesServerFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	failure := errors.New("listener failed")
	serverErrors := make(chan error, 1)
	serverErrors <- failure
	if err := waitForStartup(ctx, server.Listener.Addr().String(), server.Listener.Addr(), serverErrors); err != failure {
		t.Fatalf("server failure was replaced: %v", err)
	}
}
