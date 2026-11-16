package applebackemu

import (
	"context"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/hyperion-cs/appleseum/applekit/internal/httpx"
)

func eachMode(t *testing.T, run func(*testing.T, Mode)) {
	t.Helper()
	for _, tc := range []struct {
		name string
		mode Mode
	}{{"memory", InMemory}, {"https", HTTPS}} {
		t.Run(tc.name, func(t *testing.T) { run(t, tc.mode) })
	}
}

func newFixture(t *testing.T, mode Mode, steps ...Step) *Server {
	t.Helper()
	s, err := New(mode, steps...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

func TestRedirectsPreserveDomainCookiesAndTlsProfiles(t *testing.T) {
	eachMode(t, func(t *testing.T, mode Mode) {
		for _, profile := range []httpx.Profile{httpx.Configurator, httpx.AuthKitPostData} {
			t.Run(profile.UserAgent(), func(t *testing.T) {
				s := newFixture(t, mode,
					Step{Name: "issue cookie", Method: "GET", Url: "https://auth.itunes.apple.com/start", Header: http.Header{"Cookie": nil}, Response: Response{
						StatusCode: 302, Header: http.Header{"Location": {"https://buy.itunes.apple.com/next"}, "Set-Cookie": {"ticket=one; Domain=.itunes.apple.com; Path=/; Secure"}},
					}},
					Step{Name: "rotate cookie", Method: "GET", Url: "https://buy.itunes.apple.com/next", Header: http.Header{"Cookie": {"ticket=one"}}, Response: Response{
						StatusCode: 302, Header: http.Header{"Location": {"https://bag.itunes.apple.com/final"}, "Set-Cookie": {"ticket=two; Domain=.itunes.apple.com; Path=/; Secure"}},
					}},
					Step{Name: "use rotated cookie", Method: "GET", Url: "https://bag.itunes.apple.com/final", Header: http.Header{"Cookie": {"ticket=two"}}, Response: Response{Body: []byte("done")}},
				)
				r, _ := http.NewRequestWithContext(httpx.WithProfile(context.Background(), profile), "GET", "https://auth.itunes.apple.com/start", nil)
				response, err := s.Client().Do(r)
				if err != nil {
					t.Fatal(err)
				}
				defer response.Body.Close()
				body, err := io.ReadAll(response.Body)
				if err != nil || string(body) != "done" {
					t.Fatalf("redirect chain: %q, %v", body, err)
				}
				if err := s.Verify(); err != nil {
					t.Fatal(err)
				}
				if mode == HTTPS {
					if response.TLS == nil || len(response.TLS.VerifiedChains) == 0 {
						t.Fatal("HTTPS did not verify the certificate chain")
					}
					protocol := "HTTP/2.0"
					if profile == httpx.AuthKitPostData {
						protocol = "HTTP/1.1"
					}
					for i, host := range []string{"auth.itunes.apple.com", "buy.itunes.apple.com", "bag.itunes.apple.com"} {
						got := s.Requests()[i]
						if got.Host != host || got.ServerName != host || got.Protocol != protocol {
							t.Fatalf("routing, SNI or ALPN lost: %+v", got)
						}
					}
				}
			})
		}
	})
}

func TestStrictScenarioRemembersIgnoredErrors(t *testing.T) {
	eachMode(t, func(t *testing.T, mode Mode) {
		for _, mutation := range []string{"method", "URL", "Host", "header", "body"} {
			t.Run(mutation, func(t *testing.T) {
				s := newFixture(t, mode, Step{Name: "expected", Method: "POST", Url: "https://auth.itunes.apple.com/test", Header: http.Header{"X-Fixture": {"yes"}}, Body: []byte("expected")})
				if err := s.Verify(); err == nil {
					t.Fatal("missing request was accepted")
				}

				makeRequest := func(mutate bool) *http.Request {
					r, _ := http.NewRequest("POST", "https://auth.itunes.apple.com/test", strings.NewReader("expected"))
					r.Header.Set("X-Fixture", "yes")
					if mutate {
						switch mutation {
						case "method":
							r.Method = "PUT"
						case "URL":
							r.URL.RawQuery = "unexpected=1"
						case "Host":
							r.Host = "buy.itunes.apple.com"
						case "header":
							r.Header.Set("X-Fixture", "no")
						case "body":
							r.Body = io.NopCloser(strings.NewReader("rejected"))
						}
					}
					return r
				}

				bad, err := s.Client().Do(makeRequest(true))
				if bad != nil {
					bad.Body.Close()
				}
				if err == nil && bad.StatusCode != 500 {
					t.Fatal("unexpected request was accepted")
				}
				good, err := s.Client().Do(makeRequest(false))
				if err != nil {
					t.Fatal(err)
				}
				good.Body.Close()
				if good.StatusCode != 200 || len(s.Requests()) != 2 {
					t.Fatal("mismatch advanced the scenario or lost its request")
				}
				if err := s.Verify(); err == nil || !strings.Contains(err.Error(), "step \"expected\"") || strings.Contains(err.Error(), "consumed") {
					t.Fatalf("ignored mismatch disappeared: %v", err)
				}
			})
		}
	})
}

func TestRejectsUnlistedHostWithoutNetworkFallback(t *testing.T) {
	eachMode(t, func(t *testing.T, mode Mode) {
		s := newFixture(t, mode)
		response, err := s.Client().Get("https://unlisted.invalid/")
		if response != nil {
			response.Body.Close()
		}
		if err == nil || s.Verify() == nil {
			t.Fatal("unlisted request did not fail the scenario")
		}
		if mode == HTTPS && !strings.Contains(err.Error(), "refusing connection") {
			t.Fatalf("unlisted host reached network dialing: %v", err)
		}
	})
}

func TestHttpsRequiresMatchingTrustedCertificate(t *testing.T) {
	s := newFixture(t, HTTPS, Step{Method: "GET", Url: "https://bag.itunes.apple.com/bag.xml"})
	root, err := x509.ParseCertificate(s.server.TLS.Certificates[0].Certificate[1])
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(root)
	for _, trusted := range []bool{false, true} {
		options := httpx.ClientOptions{DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, s.server.Listener.Addr().String())
		}}
		host := "bag.itunes.apple.com"
		if trusted {
			options.RootCas = roots
			host = "auth.itunes.apple.com" // A valid chain for the wrong DNS name.
		}
		client := httpx.NewClientWithOptions(nil, options)
		t.Cleanup(client.CloseIdleConnections)
		_, err := client.Get("https://" + host + "/bag.xml")
		var unknown x509.UnknownAuthorityError
		var mismatch x509.HostnameError
		if (!trusted && !errors.As(err, &unknown)) || (trusted && !errors.As(err, &mismatch)) {
			t.Fatalf("certificate verification bypassed: %v", err)
		}
	}
	if len(s.Requests()) != 0 {
		t.Fatal("invalid TLS reached the HTTP handler")
	}
}

func TestRequestJournalDoesNotShareMutableData(t *testing.T) {
	step := Step{Method: "POST", Url: "https://auth.itunes.apple.com/test", Header: http.Header{"X-Fixture": {"original"}}, Body: []byte("request"), Response: Response{Body: []byte("response")}}
	s := newFixture(t, InMemory, step)
	step.Header.Set("X-Fixture", "changed")
	step.Body[0] = '!'
	step.Response.Body[0] = '!'
	r, _ := http.NewRequest("POST", step.Url, strings.NewReader("request"))
	r.Header.Set("X-Fixture", "original")
	response, err := s.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if string(body) != "response" || s.Verify() != nil {
		t.Fatal("caller changed the stored scenario")
	}
	requests := s.Requests()
	requests[0].Header.Set("X-Fixture", "changed")
	requests[0].Body[0] = '!'
	if got := s.Requests()[0]; got.Header.Get("X-Fixture") != "original" || string(got.Body) != "request" {
		t.Fatal("caller changed the stored journal")
	}
}
