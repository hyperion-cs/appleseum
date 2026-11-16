package hosting

import (
	"context"
	"errors"
	"net"
	"time"
)

const (
	routeAddressTimeout = 2 * time.Second
)

// These numeric destinations select internet and local multicast routes;
// no DNS query or datagram is sent. They may reveal different local addresses,
// but cannot enumerate every interface in environments such as iSH.
func routeAddresses(ctx context.Context) []AddressRoute {
	ctx, cancel := context.WithTimeout(ctx, routeAddressTimeout)
	defer cancel()
	var routes []AddressRoute
	for _, target := range []struct{ network, address string }{
		{"udp4", "8.8.8.8:80"},
		{"udp6", "[2001:4860:4860::8888]:80"},
		{"udp4", "224.0.0.1:80"},
	} {
		if ctx.Err() != nil {
			break
		}
		ip, err := routeAddress(ctx, target.network, target.address)
		routes = append(routes, AddressRoute{Network: target.network, Destination: target.address, Ip: ip, Err: err})
	}
	return routes
}

func routeAddress(ctx context.Context, network, destination string) (net.IP, error) {
	connection, err := (&net.Dialer{}).DialContext(ctx, network, destination)
	if err != nil {
		return nil, err
	}
	defer connection.Close()
	address, ok := connection.LocalAddr().(*net.UDPAddr)
	if !ok || address.IP.To16() == nil || address.IP.IsUnspecified() || address.Zone != "" {
		return nil, errors.New("no usable local address for route")
	}
	return address.IP, nil
}
