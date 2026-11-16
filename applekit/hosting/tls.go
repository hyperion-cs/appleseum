package hosting

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"sync"
	"time"
)

// Tls creates a TLS configuration with a self-signed certificate for local OTA manifests.
// Hosts must contain the DNS names and IP addresses returned by Config.Hosts.
// The key and certificate stay in memory; the 24-hour certificate is renewed
// on a handshake when fewer than five minutes remain.
func Tls(hosts []string) (*tls.Config, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	cert := &x509.Certificate{
		Subject:               pkix.Name{CommonName: "IPA local"},
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	for _, host := range hosts {
		if ip := net.ParseIP(host); ip != nil {
			cert.IPAddresses = append(cert.IPAddresses, ip)
		} else {
			cert.DNSNames = append(cert.DNSNames, host)
		}
	}

	var mu sync.Mutex
	var cached *tls.Certificate
	var expires time.Time

	issue := func() error {
		now := time.Now()
		current := *cert
		current.NotBefore = now.Add(-5 * time.Minute)
		current.NotAfter = now.Add(24 * time.Hour)
		serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
		if err != nil {
			return err
		}
		current.SerialNumber = serial
		der, err := x509.CreateCertificate(rand.Reader, &current, &current, &key.PublicKey, key)
		if err != nil {
			return err
		}
		cached = &tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
		expires = current.NotAfter
		return nil
	}

	if err := issue(); err != nil {
		return nil, err
	}
	return &tls.Config{MinVersion: tls.VersionTLS12, GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
		mu.Lock()
		defer mu.Unlock()
		if time.Now().After(expires.Add(-5 * time.Minute)) {
			if err := issue(); err != nil {
				return nil, err
			}
		}
		return cached, nil
	}}, nil
}
