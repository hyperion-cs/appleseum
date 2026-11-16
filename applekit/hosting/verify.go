package hosting

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"time"
)

const (
	manifestLookupTimeout = 2 * time.Second
)

// ManifestHostError reports DNS verification details for diagnostics.
type ManifestHostError struct {
	Host      string
	Expected  net.IP
	Addresses []net.IPAddr
	Reason    string
	Err       error
}

func (e *ManifestHostError) Error() string {
	return fmt.Sprintf("manifest DNS verification reason=%s host=%q expected=%s addresses=%v error=%v", e.Reason, e.Host, e.Expected, e.Addresses, e.Err)
}

func (e *ManifestHostError) Unwrap() error { return e.Err }

func (h Config) manifestHostCheck(r Request) (string, net.IP) {
	if !h.Ish || h.Public || h.Resolver != Nip {
		return "", nil
	}
	u, _ := url.Parse(h.url(r, true, "/"))
	host := u.Hostname()
	if !h.ManifestOnlyHost(host) {
		return "", nil
	}
	return host, h.nipAddress(host)
}

// RequiresManifestHostCheck reports whether this request emits a nip.io alias.
func (h Config) RequiresManifestHostCheck(r Request) bool {
	host, _ := h.manifestHostCheck(r)
	return host != ""
}

// VerifyManifestHost checks a nip.io alias using the caller's system DNS.
// The result is a point-in-time check, not a DNS pin for the system installer.
func (h Config) VerifyManifestHost(ctx context.Context, r Request, lookup func(context.Context, string) ([]net.IPAddr, error)) error {
	host, expected := h.manifestHostCheck(r)
	if host == "" {
		return nil
	}
	failure := &ManifestHostError{Host: host, Expected: expected}
	if expected == nil {
		failure.Reason = "unexpected_host"
		return failure
	}
	ctx, cancel := context.WithTimeout(ctx, manifestLookupTimeout)
	defer cancel()
	if lookup == nil {
		// Fail closed on partial A/AAAA failures. No public-DNS fallback or app cache.
		lookup = (&net.Resolver{PreferGo: true, StrictErrors: true}).LookupIPAddr
	}
	// The trailing dot prevents resolver search domains from changing the name.
	addresses, err := lookup(ctx, host+".")
	failure.Addresses, failure.Err = addresses, err
	if err != nil || ctx.Err() != nil {
		if err == nil {
			failure.Err = ctx.Err()
		}
		var dnsError net.Error
		switch {
		case errors.Is(ctx.Err(), context.DeadlineExceeded), errors.Is(err, context.DeadlineExceeded), errors.As(err, &dnsError) && dnsError.Timeout():
			failure.Reason = "resolve_timeout"
		case errors.Is(ctx.Err(), context.Canceled), errors.Is(err, context.Canceled):
			failure.Reason = "canceled"
		default:
			failure.Reason = "resolve_failed"
		}
		return failure
	}
	if len(addresses) == 0 {
		failure.Reason = "empty_answer"
		return failure
	}
	for _, address := range addresses {
		if address.Zone != "" || !address.IP.Equal(expected) {
			failure.Reason = "unexpected_ip"
			return failure
		}
	}
	return nil
}
