package hosting

import (
	"context"
	"net"
	"slices"
)

// AddressDiscovery preserves every usable address and a shorter suggested list.
type AddressDiscovery struct {
	All, Preferred []net.IP
	InterfaceError error
	Routes         []AddressRoute
}

// AddressRoute describes a route probe without sending a datagram.
type AddressRoute struct {
	Network, Destination string
	Ip                   net.IP
	Err                  error
}

// DiscoverAddresses uses explicit listeners, interface discovery, then route fallbacks.
func DiscoverAddresses(ctx context.Context, listeners ...string) AddressDiscovery {
	if ips := explicitListenIps(listeners...); len(ips) > 0 {
		return AddressDiscovery{All: ips, Preferred: ips}
	}
	all, preferred, err := interfaceAddresses()
	if err == nil {
		return AddressDiscovery{All: all, Preferred: preferred}
	}
	result := AddressDiscovery{All: []net.IP{net.IPv4(127, 0, 0, 1)}, InterfaceError: err}
	add := func(ip net.IP) {
		if ip != nil && !ip.IsUnspecified() && !slices.ContainsFunc(result.All, ip.Equal) {
			result.All = append(result.All, ip)
		}
	}
	for _, address := range listeners {
		host, _, _ := net.SplitHostPort(address)
		add(net.ParseIP(host))
	}
	result.Routes = routeAddresses(ctx)
	for _, route := range result.Routes {
		if route.Err == nil {
			add(route.Ip)
		}
	}
	result.Preferred = slices.Clone(result.All)
	return result
}

func explicitListenIps(addresses ...string) []net.IP {
	var ips []net.IP
	for _, address := range addresses {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return nil
		}
		ip := net.ParseIP(host)
		if ip == nil || ip.IsUnspecified() {
			return nil
		}
		ips = append(ips, ip)
	}
	return ips
}
