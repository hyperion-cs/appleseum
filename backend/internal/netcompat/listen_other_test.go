//go:build !linux

package netcompat

import (
	"net"
	"testing"
)

func TestOtherPlatformsReturnStandardListener(t *testing.T) {
	listener, err := Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if _, ok := listener.(*net.TCPListener); !ok {
		t.Fatalf("expected standard TCPListener, got %T", listener)
	}
}
