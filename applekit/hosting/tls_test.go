package hosting_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"
	"runtime"
	"testing"

	"github.com/hyperion-cs/appleseum/applekit/hosting"
)

func TestCertificateRemainsStableAcrossConnections(t *testing.T) {
	cfg, err := hosting.Tls([]string{"localhost", "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	first, err := cfg.GetCertificate(&tls.ClientHelloInfo{ServerName: "localhost"})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		next, err := cfg.GetCertificate(&tls.ClientHelloInfo{ServerName: "localhost"})
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(first.Certificate[0], next.Certificate[0]) {
			t.Fatal("certificate changed between connections")
		}
	}
}

func TestCertificateNames(t *testing.T) {
	hosts := []string{"localhost", "127.0.0.1", "::1"}
	cfg, err := hosting.Tls(hosts)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := cfg.GetCertificate(&tls.ClientHelloInfo{})
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(encoded.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(cert)
	for _, host := range hosts {
		if _, err := cert.Verify(x509.VerifyOptions{DNSName: host, Roots: roots}); err != nil {
			t.Fatalf("certificate does not cover %s: %v", host, err)
		}
	}
	if err := cert.VerifyHostname("evil.example.test"); err == nil {
		t.Fatal("certificate covers an unlisted domain")
	}
}

func TestIshHostCertificate(t *testing.T) {
	hosts := (hosting.Config{Resolver: hosting.Nip, Ish: true, LocalHosts: []string{"localhost", "127.0.0.1", "192.168.50.10", "::1"}}).Hosts()
	config, err := hosting.Tls(hosts)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := config.GetCertificate(&tls.ClientHelloInfo{})
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(encoded.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, host := range hosts {
		if err := certificate.VerifyHostname(host); err != nil {
			t.Fatal(err)
		}
	}
}

func TestConsoleFilteringRetainsAllIpsInCertificate(t *testing.T) {
	discovery := hosting.DiscoverAddresses(context.Background())
	all, console, err := discovery.All, discovery.Preferred, discovery.InterfaceError
	if err != nil {
		t.Fatal(err)
	}

	contains := func(ips []net.IP, ip net.IP) bool {
		for _, item := range ips {
			if item.Equal(ip) {
				return true
			}
		}
		return false
	}

	if len(all) == 0 || !contains(console, net.IPv4(127, 0, 0, 1)) {
		t.Fatal("loopback link missing")
	}
	for _, ip := range console {
		if !contains(all, ip) {
			t.Fatal("console link is not a local address")
		}
	}
	// The complete inventory must still include every non-loopback IPv4,
	// including Docker networks using 172.16/12, before TLS is configured.
	// Go's netlink inventory is restricted on Android; discovery uses libc there.
	if runtime.GOOS != "android" {
		addresses, err := net.InterfaceAddrs()
		if err != nil {
			t.Fatal(err)
		}
		for _, address := range addresses {
			ip, _, err := net.ParseCIDR(address.String())
			if err == nil && ip.To4() != nil && !ip.IsLoopback() && !ip.IsUnspecified() && !contains(all, ip) {
				t.Fatalf("interface IP %s removed from the complete inventory", ip)
			}
		}
	}
	var hosts []string
	for _, ip := range all {
		hosts = append(hosts, ip.String())
	}
	cfg, err := hosting.Tls(hosts)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := cfg.GetCertificate(&tls.ClientHelloInfo{})
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(encoded.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, ip := range all {
		if err := cert.VerifyHostname(ip.String()); err != nil {
			t.Fatal("local IP lost from certificate", err)
		}
	}
}
