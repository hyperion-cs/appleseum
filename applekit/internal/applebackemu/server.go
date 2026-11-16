// Package applebackemu provides a synthetic Apple HTTP backend for offline tests.
// Scenarios use an isolated transport or verified local HTTPS.
// Server cryptography is independent of client implementations.
// The emulator is not a runtime dependency of applekit.
package applebackemu

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"sync"

	"github.com/hyperion-cs/appleseum/applekit/internal/httpx"
)

const (
	InMemory Mode = iota
	HTTPS
)

type Mode uint8

// Step is one expected request and its synthetic response. URL includes the
// query. Header lists required values; an empty slice requires absence. A nil
// Body skips body matching, while a non-nil empty Body requires an empty body.
type Step struct {
	Name     string
	Method   string
	Url      string
	Header   http.Header
	Body     []byte
	Response Response
	// Optional steps may be omitted as a trailing suffix, in listed order.
	// New rejects a required step after an optional one.
	Optional bool
	// Check validates structured payloads after the ordinary request checks.
	Check func(Request) error
	// Respond overrides Response. Calls are serialized and must not reenter this Server.
	// Stateful callbacks must not be shared across scenarios.
	Respond func(Request) (Response, error)
}

type Response struct {
	StatusCode int // Zero means 200.
	Header     http.Header
	Body       []byte
	// OpenBody overrides Body with a fresh stream per response. The receiver
	// closes it; streamed faults abort HTTPS responses after headers are sent.
	OpenBody func(context.Context) io.ReadCloser
	// Nil infers len(Body), or -1 for a stream. -1 means unknown length.
	ContentLength *int64
}

// Request is a snapshot, including TLS observations made by the HTTPS server.
type Request struct {
	Method, Url, Host string
	Header            http.Header
	Body              []byte
	Protocol          string
	ServerName        string
	TlsVersion        uint16
}

// Server consumes steps in order. Requests and verification are concurrency safe;
// a scenario with concurrent independent requests should use separate servers.
type Server struct {
	mu       sync.Mutex
	steps    []Step
	next     int
	requests []Request
	failures []error
	client   *http.Client
	server   *httptest.Server
}

// New creates an isolated scenario with no internet access.
// Call Verify to check all steps and Close to release the client and listener.
func New(mode Mode, steps ...Step) (*Server, error) {
	if mode != InMemory && mode != HTTPS {
		return nil, fmt.Errorf("applebackemu: unknown mode %d", mode)
	}
	s := &Server{}
	hosts := map[string]bool{}
	optionalTail := false
	for i, step := range steps {
		if optionalTail && !step.Optional {
			return nil, fmt.Errorf("applebackemu: required step %d follows an optional step", i+1)
		}
		optionalTail = step.Optional
		u, err := url.Parse(step.Url)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" || (u.Port() != "" && u.Port() != "443") {
			return nil, fmt.Errorf("applebackemu: step %d requires an absolute HTTPS URL on port 443", i+1)
		}
		if step.Method == "" || (step.Response.StatusCode != 0 && (step.Response.StatusCode < 200 || step.Response.StatusCode > 599)) {
			return nil, fmt.Errorf("applebackemu: step %d has an invalid method or response status", i+1)
		}
		if step.Response.ContentLength != nil && *step.Response.ContentLength < -1 {
			return nil, fmt.Errorf("applebackemu: step %d has an invalid content length", i+1)
		}
		hosts[u.Hostname()] = true
		step.Header = step.Header.Clone()
		step.Body = bytes.Clone(step.Body)
		step.Response.Header = step.Response.Header.Clone()
		step.Response.Body = bytes.Clone(step.Response.Body)
		if step.Response.ContentLength != nil {
			length := *step.Response.ContentLength
			step.Response.ContentLength = &length
		}
		s.steps = append(s.steps, step)
	}
	if mode == InMemory {
		s.client = httpx.NewClient(nil)
		s.client.Transport = memoryTransport{s}
		return s, nil
	}
	certificate, roots, err := testCertificate(hosts)
	if err != nil {
		return nil, err
	}
	s.server = httptest.NewUnstartedServer(http.HandlerFunc(s.serveHttp))
	s.server.EnableHTTP2 = true
	s.server.TLS = &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12}
	s.server.Config.ErrorLog = log.New(io.Discard, "", 0)
	s.server.StartTLS()
	s.client = httpx.NewClientWithOptions(nil, httpx.ClientOptions{
		RootCas: roots,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil || port != "443" || !hosts[host] {
				err := fmt.Errorf("applebackemu: refusing connection to unlisted address %q", addr)
				s.mu.Lock()
				s.failures = append(s.failures, err)
				s.mu.Unlock()
				return nil, err
			}
			return (&net.Dialer{}).DialContext(ctx, network, s.server.Listener.Addr().String())
		},
	})
	return s, nil
}

func (s *Server) Client() *http.Client { return s.client }

func (s *Server) Close() {
	s.client.CloseIdleConnections()
	if s.server != nil {
		s.server.Close()
	}
}

func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	requests := slices.Clone(s.requests)
	for i := range requests {
		requests[i].Header = requests[i].Header.Clone()
		requests[i].Body = bytes.Clone(requests[i].Body)
	}
	return requests
}

// Verify also remembers failures when a caller ignored the HTTP error.
func (s *Server) Verify() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	failures := slices.Clone(s.failures)
	if s.next != len(s.steps) && !s.steps[s.next].Optional {
		failures = append(failures, fmt.Errorf("applebackemu: consumed %d of %d steps; next is %q", s.next, len(s.steps), s.steps[s.next].Name))
	}
	return errors.Join(failures...)
}

func (s *Server) dispatch(r *http.Request) (Response, error) {
	u := *r.URL
	if u.Scheme == "" {
		u.Scheme = "https"
		u.Host = r.Host
	}
	request := Request{Method: r.Method, Url: u.String(), Host: r.Host, Header: r.Header.Clone(), Protocol: r.Proto}
	if request.Host == "" {
		request.Host = u.Host
	}
	var err error
	if r.Body != nil {
		request.Body, err = io.ReadAll(r.Body)
	}
	if r.TLS != nil {
		request.ServerName, request.TlsVersion = r.TLS.ServerName, r.TLS.Version
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, request)
	if err == nil {
		err = r.Context().Err()
	}
	if err == nil {
		if s.next == len(s.steps) {
			err = fmt.Errorf("unexpected request %s %s after scenario completed", request.Method, request.Url)
		} else {
			err = match(s.steps[s.next], request)
		}
	}
	if err != nil {
		err = fmt.Errorf("applebackemu: request %d: %w", len(s.requests), err)
		s.failures = append(s.failures, err)
		return Response{}, err
	}
	step := s.steps[s.next]
	response := step.Response
	if step.Respond != nil {
		response, err = step.Respond(cloneRequest(request))
		if err != nil {
			err = fmt.Errorf("applebackemu: step %q: %w", step.Name, err)
			s.failures = append(s.failures, err)
			return Response{}, err
		}
	}
	s.next++
	if response.StatusCode == 0 {
		response.StatusCode = http.StatusOK
	}
	return response, nil
}

func match(step Step, request Request) error {
	u, _ := url.Parse(step.Url) // Validated by New.
	if request.Method != step.Method || request.Url != step.Url || request.Host != u.Host {
		return fmt.Errorf("step %q: expected %s %s (Host %s), got %s %s (Host %s)", step.Name, step.Method, step.Url, u.Host, request.Method, request.Url, request.Host)
	}
	for name, values := range step.Header {
		if !slices.Equal(request.Header.Values(name), values) {
			return fmt.Errorf("step %q: header %s mismatch", step.Name, name)
		}
	}
	if step.Body != nil && !bytes.Equal(step.Body, request.Body) {
		return fmt.Errorf("step %q: body mismatch", step.Name)
	}
	if step.Check != nil {
		if err := step.Check(cloneRequest(request)); err != nil {
			return fmt.Errorf("step %q: %w", step.Name, err)
		}
	}
	return nil
}

func cloneRequest(request Request) Request {
	request.Header = request.Header.Clone()
	request.Body = bytes.Clone(request.Body)
	return request
}

func (s *Server) serveHttp(w http.ResponseWriter, r *http.Request) {
	response, err := s.dispatch(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	body, length, err := s.openBody(r.Context(), response)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer body.Close()
	for name, values := range response.Header {
		w.Header()[name] = slices.Clone(values)
	}
	if length >= 0 {
		w.Header().Set("Content-Length", fmt.Sprint(length))
	} else {
		w.Header().Del("Content-Length")
	}
	w.WriteHeader(response.StatusCode)
	if length < 0 || response.OpenBody != nil {
		// Prevent net/http from inferring a short stream's size after buffering.
		if flush, ok := w.(http.Flusher); ok {
			flush.Flush()
		}
	}
	if _, err := io.Copy(w, body); err != nil {
		if flush, ok := w.(http.Flusher); ok {
			flush.Flush()
		}
		panic(http.ErrAbortHandler)
	}
}

func (s *Server) openBody(ctx context.Context, response Response) (io.ReadCloser, int64, error) {
	length := int64(len(response.Body))
	if response.OpenBody != nil {
		length = -1
	}
	if response.ContentLength != nil {
		length = *response.ContentLength
	}
	var body io.ReadCloser
	if length >= -1 {
		if response.OpenBody == nil {
			body = io.NopCloser(bytes.NewReader(response.Body))
		} else {
			body = response.OpenBody(ctx)
		}
	}
	if body == nil {
		err := fmt.Errorf("applebackemu: invalid response stream or content length")
		s.mu.Lock()
		s.failures = append(s.failures, err)
		s.mu.Unlock()
		return nil, 0, err
	}
	return body, length, nil
}

type memoryTransport struct{ server *Server }

func (m memoryTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Body != nil {
		defer r.Body.Close()
	}
	// Match the real httpx transport's default User-Agent without opening a socket.
	if r.Header.Get("User-Agent") == "" {
		r = r.Clone(r.Context())
		r.Header.Set("User-Agent", httpx.UserAgentFor(r.Context()))
	}
	response, err := m.server.dispatch(r)
	if err != nil {
		return nil, err
	}
	body, length, err := m.server.openBody(r.Context(), response)
	if err != nil {
		return nil, err
	}
	return &http.Response{
		StatusCode: response.StatusCode,
		Status:     fmt.Sprintf("%d %s", response.StatusCode, http.StatusText(response.StatusCode)),
		Header:     response.Header.Clone(), Body: body,
		ContentLength: length, Request: r,
	}, nil
}
