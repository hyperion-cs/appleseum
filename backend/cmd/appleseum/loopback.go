package main

import (
	"context"
	"crypto/tls"
	"net"
	"time"

	"appleseum/backend/internal/rawlog"
)

const (
	loopbackCheckTimeout = time.Second
)

func canReachIpv6Loopback(ctx context.Context, bound net.Addr, useTls bool) bool {
	address, ok := bound.(*net.TCPAddr)
	if !ok || address.IP.To4() != nil || (!address.IP.IsUnspecified() && !address.IP.Equal(net.IPv6loopback)) {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, loopbackCheckTimeout)
	defer cancel()
	target := (&net.TCPAddr{IP: net.IPv6loopback, Port: address.Port}).String()
	connection, err := (&net.Dialer{}).DialContext(ctx, "tcp6", target)
	if err == nil {
		defer connection.Close()
		if useTls {
			// Check our own self-signed listener. Finish the handshake so this
			// reachability check does not cause a spurious TLS failure in logs.
			client := tls.Client(connection, &tls.Config{InsecureSkipVerify: true})
			err = client.HandshakeContext(ctx)
		}
	}
	rawlog.Log("IPv6 loopback check address=%s tls=%t error=%s", target, useTls, rawlog.Error(err))
	return err == nil
}
