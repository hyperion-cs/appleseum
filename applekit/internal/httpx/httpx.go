// Package httpx provides shared, pooled HTTP with Apple client TLS profiles.
package httpx

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"

	"github.com/hyperion-cs/appleseum/applekit/internal/dns"
	utls "github.com/refraction-networking/utls"
	"golang.org/x/net/publicsuffix"
)

const (
	DefaultRequestTimeout      = 90 * time.Second
	DefaultDialTimeout         = 30 * time.Second
	DefaultTcpKeepAlive        = dns.DefaultTcpKeepAlive
	DefaultIdleConnTimeout     = 90 * time.Second
	DefaultMaxIdleConns        = 32
	DefaultMaxIdleConnsPerHost = 4
	DefaultMaxConnsPerHost     = 4
)

// NewClient isolates pools by TLS profile; a nil jar creates a private cookie jar.
func NewClient(jar http.CookieJar) *http.Client {
	return NewClientWithOptions(jar, ClientOptions{})
}

// ClientOptions overrides network routing and trust for an isolated client.
// Nil fields retain production dialing and host-specific certificate roots.
type ClientOptions struct {
	DialContext func(context.Context, string, string) (net.Conn, error)
	RootCas     *x509.CertPool
}

// NewClientWithOptions retains the real TLS profiles, verification and pools.
func NewClientWithOptions(jar http.CookieJar, options ClientOptions) *http.Client {
	if options.RootCas != nil {
		options.RootCas = options.RootCas.Clone()
	}
	if jar == nil {
		jar, _ = cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
	}
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetHTTP2(true)
	rt := &roundTripper{}
	for i := range rt.transports {
		profile := Profile(i)
		rt.transports[i] = &http.Transport{
			DialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return profile.dialTls(ctx, network, addr, options)
			},
			Protocols:           protocols,
			DisableCompression:  true, // Preserve the actor's explicit Accept-Encoding.
			MaxIdleConns:        DefaultMaxIdleConns,
			MaxIdleConnsPerHost: DefaultMaxIdleConnsPerHost,
			MaxConnsPerHost:     DefaultMaxConnsPerHost,
			IdleConnTimeout:     DefaultIdleConnTimeout,
		}
	}
	return &http.Client{Jar: jar, Timeout: DefaultRequestTimeout, Transport: rt}
}

// NoRedirect copies the client, sharing its transport and jar while changing redirect policy.
func NoRedirect(client *http.Client) *http.Client {
	copy := *client

	copy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	return &copy
}

func IsAppleHost(host string) bool {
	host = strings.ToLower(host)
	return host == "apple.com" || strings.HasSuffix(host, ".apple.com") || host == "mzstatic.com" || strings.HasSuffix(host, ".mzstatic.com")
}

func IsAppleUrl(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.User == nil && u.Fragment == "" && IsAppleHost(u.Hostname())
}

type roundTripper struct {
	transports [MacAppStore + 1]*http.Transport
}

func (rt *roundTripper) roundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Scheme != "https" {
		return nil, fmt.Errorf("httpx: only https is supported, got %q", req.URL.Scheme)
	}
	profile := profileFor(req)
	if int(profile) >= len(rt.transports) {
		return nil, fmt.Errorf("httpx: unknown client profile %d", profile)
	}
	if req.Header.Get("User-Agent") == "" {
		req = req.Clone(req.Context())
		req.Header.Set("User-Agent", UserAgentFor(req.Context()))
	}
	return rt.transports[profile].RoundTrip(req)
}

func (rt *roundTripper) CloseIdleConnections() {
	for _, transport := range rt.transports {
		transport.CloseIdleConnections()
	}
}

func (p Profile) dialTls(ctx context.Context, network, addr string, options ClientOptions) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, DefaultDialTimeout)
	defer cancel()
	dial := options.DialContext
	if dial == nil {
		dial = dns.DialContext
	}
	raw, err := dial(ctx, network, addr)
	if err != nil {
		return nil, fmt.Errorf("httpx dial: %w", err)
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		raw.Close()
		return nil, err
	}
	roots := options.RootCas
	if roots == nil {
		roots, err = rootsForHost(host)
		if err != nil {
			raw.Close()
			return nil, err
		}
	}
	conn, err := p.connectionWithRoots(raw, host, roots)
	if err != nil {
		raw.Close()
		return nil, err
	}
	deadline, _ := ctx.Deadline()
	return &tlsConn{UConn: conn, deadline: deadline}, nil
}

// Expose uTLS's negotiated state so net/http can select and pool HTTP/2.
type tlsConn struct {
	*utls.UConn
	deadline time.Time
}

// Let net/http perform the handshake and report its real start, completion and
// failures through httptrace. Retain the original combined TCP/TLS deadline.
func (c *tlsConn) HandshakeContext(ctx context.Context) error {
	ctx, cancel := context.WithDeadline(ctx, c.deadline)
	defer cancel()
	if err := c.UConn.HandshakeContext(ctx); err != nil {
		return fmt.Errorf("httpx tls handshake: %w", err)
	}
	return nil
}

func (c *tlsConn) ConnectionState() tls.ConnectionState {
	s := c.UConn.ConnectionState()
	return tls.ConnectionState{
		Version:                     s.Version,
		HandshakeComplete:           s.HandshakeComplete,
		DidResume:                   s.DidResume,
		CipherSuite:                 s.CipherSuite,
		CurveID:                     tls.CurveID(s.CurveID),
		NegotiatedProtocol:          s.NegotiatedProtocol,
		NegotiatedProtocolIsMutual:  s.NegotiatedProtocolIsMutual,
		ServerName:                  s.ServerName,
		PeerCertificates:            s.PeerCertificates,
		VerifiedChains:              s.VerifiedChains,
		SignedCertificateTimestamps: s.SignedCertificateTimestamps,
		OCSPResponse:                s.OCSPResponse,
		TLSUnique:                   s.TLSUnique,
		ECHAccepted:                 s.ECHAccepted,
		HelloRetryRequest:           s.HelloRetryRequest,
	}
}
