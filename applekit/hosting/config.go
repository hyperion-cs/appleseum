// Package hosting discovers installation hosts, builds OTA manifests and links,
// and creates self-signed TLS configurations for local manifest servers.
package hosting

import (
	"encoding/hex"
	"fmt"
	"net"
	"net/url"
	"slices"
	"strings"

	"github.com/hyperion-cs/appleseum/applekit/ipa"
)

const (
	Bonjour Resolver = "bonjour"
	Nip     Resolver = "nip"
)

// Resolver selects the hostname used for same-device installation.
// Its zero value selects Bonjour.
type Resolver string

func (r Resolver) MarshalText() ([]byte, error) { return []byte(r), nil }

// UnmarshalText accepts only the known resolvers, so configuration can be read directly into one.
func (r *Resolver) UnmarshalText(text []byte) error {
	switch value := Resolver(text); value {
	case Bonjour, Nip:
		*r = value
		return nil
	}
	return fmt.Errorf("resolver must be %s or %s", Bonjour, Nip)
}

// Config describes caller-owned HTTP endpoints without depending on its server.
// Ish enables same-device installation compatibility for iSH on iOS/iPadOS.
type Config struct {
	Ish, Public            bool
	Resolver               Resolver
	BonjourName            string
	LocalHosts             []string
	HttpPort, ManifestPort string
}

// Request contains transport facts, not forwarded HTTP headers.
type Request struct {
	Host              string
	ClientIp, LocalIp net.IP
}

// App describes an app and its caller-owned path, without constructing URLs.
type App struct {
	Metadata ipa.Metadata
	Path     string
}

// Hosts returns the exact names required by the HTTP allowlist and TLS certificate.
// It neither resolves DNS nor changes the caller's host inventory.
func (h Config) Hosts() []string {
	hosts := slices.Clone(h.LocalHosts)
	if h.Ish && !h.Public {
		for _, own := range h.LocalHosts {
			if alias := h.sameDeviceHost(net.ParseIP(own)); alias != "" && !slices.Contains(hosts, alias) {
				hosts = append(hosts, alias)
			}
		}
	}
	return hosts
}

// sameDeviceHost is the name iOS uses to reach this server on the same device
// through the local address: the Bonjour name, or that address's nip.io alias.
func (h Config) sameDeviceHost(local net.IP) string {
	if h.Resolver == Nip {
		return nipHost(local)
	}
	return h.BonjourName
}

// ManifestOnlyHost identifies normalized names reserved for local HTTPS manifests.
// Callers must still check their host allowlist before accepting these names.
func (h Config) ManifestOnlyHost(host string) bool {
	return !h.Public && (host == "nip.io" || strings.HasSuffix(host, ".nip.io"))
}

// InstallUrl returns an install link for the manifest at the supplied path.
func (h Config) InstallUrl(r Request, manifestPath string) string {
	return InstallUrl(h.url(r, true, manifestPath))
}

// Manifest returns the manifest with ready-to-use package URLs.
func (h Config) Manifest(r Request, apps []App) ([]byte, error) {
	items := make([]ManifestItem, len(apps))
	for i, app := range apps {
		items[i] = ManifestItem{Metadata: app.Metadata, Url: h.url(r, false, app.Path)}
	}
	return Manifest(items)
}

func (h Config) url(r Request, manifest bool, path string) string {
	// A path may carry a query, such as the trace id of an installation attempt.
	path, query, _ := strings.Cut(path, "?")
	if h.Public {
		return (&url.URL{Scheme: "https", Host: r.Host, Path: path, RawQuery: query}).String()
	}
	host := r.Host
	if name, _, err := net.SplitHostPort(host); err == nil {
		host = name
	}
	scheme, port := "http", h.HttpPort
	if manifest {
		scheme, port = "https", h.ManifestPort
	}
	if h.Ish {
		// A nip.io alias reached the manifest through outside DNS; the IPA itself
		// is fetched from the address it stands for.
		if ip := h.nipAddress(host); ip != nil {
			host = ip.String()
		}
		if manifest && r.ClientIp != nil && r.LocalIp != nil {
			self := r.ClientIp.IsLoopback() || r.ClientIp.Equal(r.LocalIp) || slices.ContainsFunc(h.LocalHosts, func(own string) bool {
				return r.ClientIp.Equal(net.ParseIP(own))
			})
			alias := h.sameDeviceHost(r.LocalIp)
			// Never emit a name absent from the allowlist and TLS inventory.
			if self && alias != "" && slices.Contains(h.Hosts(), alias) {
				host = alias
			}
		}
	}
	return (&url.URL{Scheme: scheme, Host: net.JoinHostPort(host, port), Path: path, RawQuery: query}).String()
}

// nipAddress is the local address that host, a nip.io alias, stands for.
func (h Config) nipAddress(host string) net.IP {
	if h.Resolver != Nip {
		return nil
	}
	host = strings.TrimSuffix(host, ".")
	for _, own := range h.LocalHosts {
		ip := net.ParseIP(own)
		if alias := nipHost(ip); alias != "" && strings.EqualFold(host, alias) {
			return ip
		}
	}
	return nil
}

func nipHost(ip net.IP) string {
	if ip == nil || ip.IsUnspecified() {
		return ""
	}
	if v4 := ip.To4(); v4 != nil {
		return hex.EncodeToString(v4) + ".nip.io"
	}
	if ip.To16() == nil {
		return ""
	}
	return "ipv6-" + strings.ReplaceAll(ip.String(), ":", "-") + ".nip.io"
}
