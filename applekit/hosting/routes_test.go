package hosting

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

func TestRouteAddressUsesLocalSocketAddressWithoutSending(t *testing.T) {
	for _, network := range []string{"udp4", "udp6"} {
		t.Run(network, func(t *testing.T) { testRouteAddress(t, network) })
	}
}

func testRouteAddress(t *testing.T, network string) {
	local := net.IPv4(127, 0, 0, 1)
	if network == "udp6" {
		local = net.IPv6loopback
	}
	listener, err := net.ListenUDP(network, &net.UDPAddr{IP: local})
	if err != nil {
		if network == "udp6" {
			t.Skipf("IPv6 loopback unavailable: %v", err)
		}
		t.Fatal(err)
	}
	defer listener.Close()
	ip, err := routeAddress(context.Background(), network, listener.LocalAddr().String())
	if err != nil || !ip.Equal(local) {
		t.Fatalf("local route address = %v, %v", ip, err)
	}
	if err := listener.SetReadDeadline(time.Now().Add(30 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	var data [1]byte
	_, _, err = listener.ReadFromUDP(data[:])
	var timeout net.Error
	if !errors.As(err, &timeout) || !timeout.Timeout() {
		t.Fatalf("route selection sent a datagram: %v", err)
	}
}

func TestRouteAddressRespectsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := routeAddress(ctx, "udp4", "127.0.0.1:80"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled route discovery = %v", err)
	}
}
