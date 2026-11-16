package applekit

import (
	"context"

	"github.com/hyperion-cs/appleseum/applekit/internal/dns"
)

// DnsEvent reports failed resolution attempts and strategy changes.
type DnsEvent = dns.Event

// WithDnsObserver adds optional DNS diagnostics without changing HTTP traffic.
func WithDnsObserver(ctx context.Context, observe func(DnsEvent)) context.Context {
	return dns.WithObserver(ctx, observe)
}
