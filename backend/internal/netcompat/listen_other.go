//go:build !linux

package netcompat

import "net"

func wrapListener(listener net.Listener) net.Listener { return listener }
