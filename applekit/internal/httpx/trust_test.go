package httpx

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"testing"
)

func TestGrandSlamTrustAnchor(t *testing.T) {
	root := decodeCertificate(t, appleRoot)
	digest := sha256.Sum256(root.Raw)
	if hex.EncodeToString(digest[:]) != "b0b1730ecbc7ff4505142c49f1295e6eda6bcaed7e2c68c5be91b5a11001f024" || !root.IsCA {
		t.Fatal("unexpected GrandSlam trust anchor")
	}
	for _, host := range []string{"gsa.apple.com", "gsas.apple.com", "GSA.APPLE.COM."} {
		roots, err := rootsForHost(host)
		if err != nil || roots == nil {
			t.Fatalf("GrandSlam roots for %s: %v", host, err)
		}
	}
	for _, host := range []string{"auth.itunes.apple.com", "www.apple.com", "example.com", "gsa.apple.com.example.com", "evilgsa.apple.com"} {
		roots, err := rootsForHost(host)
		if err != nil || roots != nil {
			t.Fatalf("custom roots escaped GrandSlam hosts: %s", host)
		}
	}
}

func decodeCertificate(t *testing.T, data []byte) *x509.Certificate {
	t.Helper()
	block, rest := pem.Decode(data)
	if block == nil || block.Type != "CERTIFICATE" || len(rest) != 0 {
		t.Fatal("invalid certificate fixture")
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return certificate
}
