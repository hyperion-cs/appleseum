package httpx

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type captureTestWriter struct {
	bytes.Buffer
	closes int
}

func (w *captureTestWriter) Close() error { w.closes++; return nil }

func TestExchangeCaptureStreamsCompleteResponse(t *testing.T) {
	payload := bytes.Repeat([]byte("full-response-marker"), 100_000)
	output := &captureTestWriter{}
	var events []RequestEvent
	body := &observedBody{ReadCloser: io.NopCloser(bytes.NewReader(payload)), capture: &ExchangeCapture{WriteCloser: output, Filename: "fixture.http"}, observe: func(e RequestEvent) { events = append(events, e) }}
	read, err := io.ReadAll(body)
	body.Close()
	if err != nil || !bytes.Equal(read, payload) || !bytes.Equal(output.Bytes(), payload) || output.closes != 1 {
		t.Fatal("exchange changed or truncated")
	}
	if len(events) != 1 || events[0].ExchangeFile != "fixture.http" || events[0].ResponseBytes != int64(len(payload)) || events[0].Truncated || len(events[0].ResponseBody) != 0 {
		t.Fatal("response was accumulated or not logged")
	}
}

func TestObservedResponseBodyIsExactAndBounded(t *testing.T) {
	for _, size := range []int{0, 32, MaxCapturedBody + 100} {
		payload := bytes.Repeat([]byte("x"), size)
		var events []RequestEvent
		body := &observedBody{ReadCloser: io.NopCloser(bytes.NewReader(payload)), observe: func(e RequestEvent) { events = append(events, e) }}
		read, err := io.ReadAll(body)
		if err != nil || !bytes.Equal(read, payload) {
			t.Fatal("capture altered response")
		}
		body.Close()
		if len(events) != 1 || events[0].ResponseBytes != int64(size) || len(events[0].ResponseBody) != min(size, MaxCapturedBody) || events[0].Truncated != (size > MaxCapturedBody) {
			t.Fatal("invalid bounded capture")
		}
	}
}

func TestRequestObserverRoutingAndIsolation(t *testing.T) {
	var seen []RequestEvent
	ctx := WithRequestObserver(context.Background(), func(e RequestEvent) { seen = append(seen, e) })
	client := NewClient(nil)
	for _, test := range []struct{ method, address, host, path string }{
		{"POST", "http://gsa.apple.com/grandslam/GsService2?password=private-marker", "gsa.apple.com", "/grandslam/GsService2"},
		{"PATCH", "http://assets.example.invalid:8043/Mixed%2FPath?key=private-marker", "assets.example.invalid:8043", "/Mixed%2FPath"},
		{"HEAD", "http://init.itunes.apple.com", "init.itunes.apple.com", "/"},
	} {
		req, _ := http.NewRequestWithContext(ctx, test.method, test.address, strings.NewReader("private-marker"))
		req.Header.Set("Authorization", "private-marker")
		_, err := client.Do(req)
		if err == nil {
			t.Fatal("observer replaced HTTPS-only shared transport")
		}
		event := seen[len(seen)-1]
		if event.Method != test.method || event.Host != test.host || event.Path != test.path || event.Error != "transport" {
			t.Fatalf("request routing changed: %+v", event)
		}
		encoded, _ := json.Marshal(event)
		if strings.Contains(string(encoded), "private-marker") || req.URL.String() != test.address {
			t.Fatal("query/body/header leaked or original request changed")
		}
	}
	other, _ := http.NewRequest("POST", "http://init.itunes.apple.com/bag.xml", nil)
	client.Do(other)
	if len(seen) != 3 {
		t.Fatal("observer leaked to unrelated context")
	}
}

func TestExchangeCaptureUsesSelectedUserAgent(t *testing.T) {
	for _, profile := range []Profile{Accounts, NativeAuthentication, AuthKitPostData, AmsAccounts} {
		const address = "http://example.invalid/path?first=one&first=two"

		output := &captureTestWriter{}
		ctx := WithProfile(context.Background(), profile)
		ctx = WithRequestObserver(ctx, func(RequestEvent) {})
		ctx = WithExchangeCapture(ctx, func() *ExchangeCapture {
			return &ExchangeCapture{WriteCloser: output, Filename: "fixture.http"}
		})
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
		_, err := NewClient(nil).Do(req) // Rejected before dialing; the request is still captured.
		if err == nil {
			t.Fatal("HTTP accepted")
		}
		if !strings.Contains(output.String(), "User-Agent: "+profile.UserAgent()+"\r\n") {
			t.Fatalf("capture has wrong User-Agent for profile %d", profile)
		}
		if !strings.HasPrefix(output.String(), "GET "+address+" HTTP/1.1\r\n") {
			t.Fatal("capture omitted the exact absolute URL")
		}
		if req.Header.Get("User-Agent") != "" || req.RequestURI != "" || req.URL.String() != address {
			t.Fatal("original request mutated")
		}
	}
}

func TestMetadataObserverDoesNotCaptureBodies(t *testing.T) {
	var events []RequestEvent
	ctx := WithExchangeCapture(context.Background(), func() *ExchangeCapture {
		t.Fatal("metadata observer created an exchange capture")
		return nil
	})
	ctx = WithRequestMetadataObserver(ctx, func(e RequestEvent) { events = append(events, e) })
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://example.invalid/path", nil)
	if _, err := NewClient(nil).Do(req); err == nil {
		t.Fatal("HTTP accepted")
	}
	if len(events) != 1 || events[0].Host != "example.invalid" || events[0].Error != "transport" {
		t.Fatalf("missing request metadata: %+v", events)
	}
	observer := ctx.Value(observerKey{}).(requestObserver)
	events = nil
	payload := strings.Repeat("x", MaxCapturedBody+100)
	body := &observedBody{ReadCloser: io.NopCloser(strings.NewReader(payload)), metadataOnly: observer.metadataOnly, observe: observer.observe, event: RequestEvent{Status: 200}}
	if n, err := io.Copy(io.Discard, body); err != nil || n != int64(len(payload)) {
		t.Fatalf("body changed: bytes=%d error=%v", n, err)
	}
	body.Close()
	if body.buffer.Cap() != 0 || len(events) != 1 || events[0].Status != 200 || events[0].ResponseBytes != int64(len(payload)) || events[0].ResponseBody != nil || events[0].Truncated {
		t.Fatalf("body accumulated or metadata lost: buffer=%d events=%+v", body.buffer.Cap(), events)
	}
}
