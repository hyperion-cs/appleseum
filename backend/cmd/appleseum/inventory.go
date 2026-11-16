package main

import (
	"context"
	"log"
	"net"

	"github.com/hyperion-cs/appleseum/applekit/hosting"

	"appleseum/backend/internal/rawlog"
)

type hostInventory struct {
	hosts   []string
	console []net.IP
	// bonjour is this device's Bonjour name; bonjourErr says why it could not be found.
	bonjour    string
	bonjourErr error
}

func inventory(ctx context.Context, o options, ish bool) hostInventory {
	discover := rawlog.Begin("startup discover addresses")
	addresses := hosting.DiscoverAddresses(ctx, o.addresses()...)
	discover()
	rawlog.Log("interface discovery error=%s", rawlog.Error(addresses.InterfaceError))
	for _, route := range addresses.Routes {
		rawlog.Log("route address network=%s destination=%s ip=%s error=%s", route.Network, route.Destination, route.Ip, rawlog.Error(route.Err))
	}
	if addresses.InterfaceError != nil {
		if len(addresses.All) > 1 {
			log.Print("Could not detect network interfaces; using localhost and available IPs")
		} else {
			log.Print("Could not detect network interfaces; using localhost only")
		}
	}
	result := hostInventory{console: addresses.Preferred}
	hosts := []string{"localhost", "::1"}
	for _, ip := range addresses.All {
		hosts = append(hosts, ip.String())
	}
	if ish && !o.public && o.resolver == hosting.Bonjour {
		discover := rawlog.Begin("startup discover Bonjour name")
		name, err := hosting.BonjourName(ctx)
		discover()
		rawlog.Log("Bonjour name=%q error=%s", name, rawlog.Error(err))
		result.bonjour, result.bonjourErr = name, err
	}
	result.hosts = (hosting.Config{
		Ish: ish, Public: o.public, LocalHosts: hosts,
		Resolver: o.resolver, BonjourName: result.bonjour,
	}).Hosts()
	return result
}
