package httpx

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"sync"
	"time"
)

const (
	MaxCapturedBody = 1_048_576
)

// Host retains any explicit port; Path is escaped and excludes the query.
// DurationMs ends at response headers, not completion of the body.
type RequestEvent struct {
	Method        string
	Host          string
	Path          string
	Status        int
	DurationMs    int64
	Error         string
	ResponseBody  []byte
	ResponseBytes int64
	Truncated     bool
	ExchangeFile  string
}

type observedBody struct {
	io.ReadCloser
	buffer       bytes.Buffer
	count        int64
	once         sync.Once
	event        RequestEvent
	observe      func(RequestEvent)
	capture      *ExchangeCapture
	metadataOnly bool
}

func (b *observedBody) finish() {
	b.once.Do(func() {
		if b.capture != nil {
			b.capture.Close()
			b.event.ExchangeFile = b.capture.Filename
		} else if !b.metadataOnly {
			b.event.ResponseBody = b.buffer.Bytes()
		}
		b.event.ResponseBytes = b.count
		b.event.Truncated = !b.metadataOnly && b.capture == nil && b.count > MaxCapturedBody
		b.observe(b.event)
	})
}

func (b *observedBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.count += int64(n)
	if b.capture != nil {
		b.capture.Write(p[:n])
	} else if left := MaxCapturedBody - b.buffer.Len(); !b.metadataOnly && left > 0 {
		b.buffer.Write(p[:min(n, left)])
	}
	if err != nil {
		if err != io.EOF {
			b.event.Error = "response_read"
		}
		b.finish()
	}
	return n, err
}

func (b *observedBody) Close() error { err := b.ReadCloser.Close(); b.finish(); return err }

type observerKey struct{}

type requestObserver struct {
	observe      func(RequestEvent)
	metadataOnly bool
}

func WithRequestObserver(ctx context.Context, observe func(RequestEvent)) context.Context {
	return context.WithValue(ctx, observerKey{}, requestObserver{observe: observe})
}

// WithRequestMetadataObserver reports timing, status and byte counts without
// buffering response bodies or creating exchange captures.
func WithRequestMetadataObserver(ctx context.Context, observe func(RequestEvent)) context.Context {
	return context.WithValue(ctx, observerKey{}, requestObserver{observe: observe, metadataOnly: true})
}

func (rt *roundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	observer, _ := req.Context().Value(observerKey{}).(requestObserver)
	observe := observer.observe
	if observe == nil {
		return rt.roundTrip(req)
	}
	var capture *ExchangeCapture
	if create, _ := req.Context().Value(captureKey{}).(func() *ExchangeCapture); create != nil && !observer.metadataOnly {
		capture = create()
		if capture != nil {
			copy := req.Clone(req.Context())
			copy.Body = nil
			copy.RequestURI = req.URL.String() // Preserve the exact absolute URL for HAR export.
			if copy.Header.Get("User-Agent") == "" {
				copy.Header.Set("User-Agent", profileFor(req).UserAgent())
			}
			header, _ := httputil.DumpRequest(copy, false)
			capture.Write(header)
			if req.Body != nil && req.GetBody != nil {
				body, err := req.GetBody()
				if err == nil {
					io.Copy(capture, body)
					body.Close()
				}
			}
			capture.Write([]byte("\r\n\r\n----- RESPONSE -----\r\n"))
		}
	}
	start := time.Now()
	resp, err := rt.roundTrip(req)
	path := req.URL.EscapedPath()
	if path == "" {
		path = "/"
	}
	event := RequestEvent{Method: req.Method, Host: req.URL.Host, Path: path, DurationMs: time.Since(start).Milliseconds()}
	if resp != nil {
		event.Status = resp.StatusCode
	}
	if err != nil {
		var dns *net.DNSError
		var network net.Error
		switch {
		case errors.Is(err, context.Canceled):
			event.Error = "canceled"
		case errors.Is(err, context.DeadlineExceeded):
			event.Error = "timeout"
		case errors.As(err, &dns):
			event.Error = "dns"
		case errors.As(err, &network) && network.Timeout():
			event.Error = "timeout"
		default:
			event.Error = "transport"
		}
	}
	if resp != nil && resp.Body != nil && err == nil {
		if capture != nil {
			header, _ := httputil.DumpResponse(resp, false)
			capture.Write(header)
		}
		resp.Body = &observedBody{ReadCloser: resp.Body, event: event, observe: observe, capture: capture, metadataOnly: observer.metadataOnly}
	} else {
		if capture != nil {
			capture.Close()
			event.ExchangeFile = capture.Filename
		}
		observe(event)
	}
	return resp, err
}

type ExchangeCapture struct {
	io.WriteCloser
	Filename string
}
type captureKey struct{}

func WithExchangeCapture(ctx context.Context, create func() *ExchangeCapture) context.Context {
	return context.WithValue(ctx, captureKey{}, create)
}
