// Package netcompat provides TCP listener compatibility for Linux environments
// without accept4, primarily iSH on iOS/iPadOS. Other platforms use net.Listen
// unchanged.
package netcompat

import "net"

// Listen is net.Listen with an automatic Linux TCP accept4 fallback.
func Listen(network, address string) (net.Listener, error) {
	listener, err := net.Listen(network, address)
	if err != nil {
		return nil, err
	}
	return wrapListener(listener), nil
}
