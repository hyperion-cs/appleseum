package hosting

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

func TestManifestDnsVerificationChecksEveryAddress(t *testing.T) {
	for _, test := range []struct {
		name, local, reason string
		addresses           []net.IPAddr
		err                 error
	}{
		{"IPv4", "127.0.0.1", "", []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil},
		{"mapped IPv4", "127.0.0.1", "", []net.IPAddr{{IP: net.ParseIP("::ffff:127.0.0.1")}}, nil},
		{"IPv6", "::1", "", []net.IPAddr{{IP: net.ParseIP("::1")}}, nil},
		{"LAN", "192.168.50.10", "", []net.IPAddr{{IP: net.ParseIP("192.168.50.10")}}, nil},
		{"foreign IPv4", "127.0.0.1", "unexpected_ip", []net.IPAddr{{IP: net.ParseIP("203.0.113.9")}}, nil},
		{"other local IP", "127.0.0.1", "unexpected_ip", []net.IPAddr{{IP: net.ParseIP("192.168.50.10")}}, nil},
		{"mixed IPv6", "127.0.0.1", "unexpected_ip", []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}, {IP: net.ParseIP("2001:db8::9")}}, nil},
		{"empty", "127.0.0.1", "empty_answer", nil, nil},
		{"partial failure", "127.0.0.1", "resolve_failed", []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, &net.DNSError{Err: "no such host", IsNotFound: true}},
		{"DNS timeout", "127.0.0.1", "resolve_timeout", nil, &net.DNSError{Err: "i/o timeout", IsTimeout: true}},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := Config{Resolver: Nip, Ish: true, LocalHosts: []string{test.local}, HttpPort: "8080", ManifestPort: "8443"}
			r := Request{Host: net.JoinHostPort(test.local, "8080"), LocalIp: net.ParseIP(test.local), ClientIp: net.ParseIP(test.local)}
			var queried string
			err := h.VerifyManifestHost(context.Background(), r, func(ctx context.Context, host string) ([]net.IPAddr, error) {
				queried = host
				deadline, ok := ctx.Deadline()
				if !ok || time.Until(deadline) > 2*time.Second || time.Until(deadline) < time.Second {
					t.Fatal("missing common two-second budget", deadline)
				}
				return test.addresses, test.err
			})
			if queried != nipHost(net.ParseIP(test.local))+"." {
				t.Fatal("wrong absolute DNS name", queried)
			}
			if test.reason == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			var failure *ManifestHostError
			if !errors.As(err, &failure) || failure.Reason != test.reason || !failure.Expected.Equal(net.ParseIP(test.local)) {
				t.Fatalf("wrong diagnostic: %v", err)
			}
			for _, address := range test.addresses {
				if !strings.Contains(err.Error(), address.IP.String()) {
					t.Fatal("missing returned address", err)
				}
			}
		})
	}
}

func TestManifestDnsVerificationScope(t *testing.T) {
	for _, test := range []struct {
		name        string
		ish, public bool
		peer        string
	}{
		{"ordinary local", false, false, "127.0.0.1"},
		{"another device", true, false, "192.168.50.11"},
		{"public", false, true, "127.0.0.1"},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := Config{Ish: test.ish, Public: test.public, LocalHosts: []string{"127.0.0.1", "192.168.50.10"}, HttpPort: "8080", ManifestPort: "8443"}
			r := Request{Host: "192.168.50.10:8080", LocalIp: net.ParseIP("192.168.50.10"), ClientIp: net.ParseIP(test.peer)}
			if h.RequiresManifestHostCheck(r) {
				t.Fatal("unnecessary DNS check")
			}
			if err := h.VerifyManifestHost(context.Background(), r, func(context.Context, string) ([]net.IPAddr, error) {
				t.Fatal("queried DNS without a same-device alias")
				return nil, nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestManifestDnsVerificationTimeout(t *testing.T) {
	h := Config{Resolver: Nip, Ish: true, LocalHosts: []string{"127.0.0.1"}, ManifestPort: "8443"}
	r := Request{Host: "127.0.0.1", LocalIp: net.ParseIP("127.0.0.1"), ClientIp: net.ParseIP("127.0.0.1")}
	started := time.Now()
	err := h.VerifyManifestHost(context.Background(), r, func(ctx context.Context, _ string) ([]net.IPAddr, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})
	var failure *ManifestHostError
	if !errors.As(err, &failure) || failure.Reason != "resolve_timeout" || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("timeout was not classified", err)
	}
	if elapsed := time.Since(started); elapsed < 1_900*time.Millisecond || elapsed > 3*time.Second {
		t.Fatal("lookup exceeded its total budget", elapsed)
	}
}

func TestManifestDnsVerificationCancellation(t *testing.T) {
	h := Config{Resolver: Nip, Ish: true, LocalHosts: []string{"127.0.0.1"}, ManifestPort: "8443"}
	r := Request{Host: "127.0.0.1", LocalIp: net.ParseIP("127.0.0.1"), ClientIp: net.ParseIP("127.0.0.1")}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := h.VerifyManifestHost(ctx, r, func(ctx context.Context, _ string) ([]net.IPAddr, error) { return nil, ctx.Err() })
	var failure *ManifestHostError
	if !errors.As(err, &failure) || failure.Reason != "canceled" {
		t.Fatal("parent cancellation was not preserved", err)
	}
}
