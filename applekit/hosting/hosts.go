package hosting

import (
	"fmt"
	"net/netip"
	"strings"

	"golang.org/x/net/idna"
)

// NormalizeHost accepts a DNS name or an IP literal, without a scheme or port.
func NormalizeHost(value string) (string, error) {
	if ip, err := netip.ParseAddr(value); err == nil && ip.Zone() == "" {
		return ip.Unmap().String(), nil
	}
	name, err := idna.Lookup.ToASCII(strings.TrimSuffix(value, "."))
	if err != nil || name == "" || len(name) > 253 {
		return "", fmt.Errorf("invalid host %q: expected a domain name or IP address without a port", value)
	}
	for _, label := range strings.Split(name, ".") {
		if len(label) == 0 || len(label) > 63 {
			return "", fmt.Errorf("invalid host %q: invalid DNS label length", value)
		}
	}
	return strings.ToLower(name), nil
}
