package httpx

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type cookieTransport func(*http.Request) (*http.Response, error)

func (f cookieTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestPublicClientRetainsAllCookiesFromRejectedResponses(t *testing.T) {
	client := NewClient(nil)
	calls := 0
	client.Transport = cookieTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		header := make(http.Header)
		status := 200
		switch calls {
		case 1:
			status = 401
			header.Add("Set-Cookie", "first=issued; Path=/; Secure")
			header.Add("Set-Cookie", "second=issued; Path=/; Secure")
		case 2:
			for _, name := range []string{"first", "second"} {
				cookie, err := r.Cookie(name)
				if err != nil || cookie.Value != "issued" {
					t.Fatalf("cookie from 401 missing: %s", name)
				}
			}
			header.Add("Set-Cookie", "first=deleted; Path=/; Secure; Max-Age=0")
			header.Add("Set-Cookie", "second=rotated; Path=/; Secure")
		case 3:
			if _, err := r.Cookie("first"); err != http.ErrNoCookie {
				t.Fatal("server deletion ignored")
			}
			if cookie, err := r.Cookie("second"); err != nil || cookie.Value != "rotated" {
				t.Fatal("server rotation ignored")
			}
		default:
			t.Fatal("unexpected request")
		}
		return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(strings.NewReader(""))}, nil
	})
	for range 3 {
		request, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://public.example.test/lookup", nil)
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
	}
}
