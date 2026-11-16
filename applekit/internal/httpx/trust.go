package httpx

import (
	"crypto/x509"
	_ "embed"
	"fmt"
	"strings"
	"sync"
)

// GrandSlam needs this extra public root, scoped to its two production hosts.
//
//go:embed assets/certs/apple-inc-root.pem
var appleRoot []byte

var grandSlamRoots = sync.OnceValues(func() (*x509.CertPool, error) {
	roots, err := x509.SystemCertPool()
	if err != nil {
		return nil, fmt.Errorf("load system certificate roots: %w", err)
	}
	if !roots.AppendCertsFromPEM(appleRoot) {
		return nil, fmt.Errorf("load embedded Apple Root CA")
	}
	return roots, nil
})

func rootsForHost(host string) (*x509.CertPool, error) {
	switch strings.TrimSuffix(strings.ToLower(host), ".") {
	case "gsa.apple.com", "gsas.apple.com":
		return grandSlamRoots()
	default:
		return nil, nil
	}
}
