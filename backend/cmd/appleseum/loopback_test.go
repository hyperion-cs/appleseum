package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"appleseum/backend/internal/netcompat"
)

func ipv6LoopbackListener(t *testing.T) net.Listener {
	t.Helper()
	listener, err := netcompat.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Skipf("IPv6 loopback is unavailable: %v", err)
	}
	t.Cleanup(func() { listener.Close() })
	return listener
}

func TestIpv6LoopbackChecksTcpAndTls(t *testing.T) {
	listener := ipv6LoopbackListener(t)
	if !canReachIpv6Loopback(context.Background(), listener.Addr(), false) {
		t.Fatal("IPv6 TCP listener was not detected")
	}
	server := httptest.NewUnstartedServer(http.NotFoundHandler())
	server.Listener.Close()
	server.Listener = ipv6LoopbackListener(t)
	server.StartTLS()
	defer server.Close()
	if !canReachIpv6Loopback(context.Background(), server.Listener.Addr(), true) {
		t.Fatal("self-signed IPv6 TLS listener was not detected")
	}
}

func TestIpv6LoopbackDoesNotAdvertiseAnIpv4OnlyListener(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if canReachIpv6Loopback(context.Background(), listener.Addr(), false) {
		t.Fatal("IPv4-only listener advertised IPv6")
	}
}

func TestIpv6LoopbackTlsRespectsDeadline(t *testing.T) {
	listener := ipv6LoopbackListener(t)
	done := make(chan struct{})
	go func() {
		defer close(done)
		connection, err := listener.Accept()
		if err == nil {
			defer connection.Close()
			io.Copy(io.Discard, connection)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if canReachIpv6Loopback(ctx, listener.Addr(), true) {
		t.Fatal("nonresponsive TLS listener advertised IPv6")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("TLS timeout left the connection open")
	}
}
