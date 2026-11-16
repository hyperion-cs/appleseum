package applebackemu

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestRangeAssetResponses(t *testing.T) {
	for _, tc := range []struct {
		name, request, wantRange, wantBody string
		status                             int
	}{
		{"suffix", "bytes=-4", "bytes 6-9/10", "6789", 206},
		{"suffix larger than asset", "bytes=-65557", "bytes 0-9/10", "0123456789", 206},
		{"last byte", "bytes=-1", "bytes 9-9/10", "9", 206},
		{"closed", "bytes=2-5", "bytes 2-5/10", "2345", 206},
		{"open ended", "bytes=7-", "bytes 7-9/10", "789", 206},
		{"clamped end", "bytes=8-99", "bytes 8-9/10", "89", 206},
		{"zero suffix", "bytes=-0", "bytes */10", "", 416},
		{"past end", "bytes=10-", "bytes */10", "", 416},
		{"reversed", "bytes=5-2", "bytes */10", "", 416},
		{"missing", "", "bytes */10", "", 416},
		{"wrong unit", "items=0-1", "bytes */10", "", 416},
		{"multiple ranges", "bytes=0-1,3-4", "bytes */10", "", 416},
		{"signed start", "bytes=+1-3", "bytes */10", "", 416},
		{"overflow", "bytes=-9223372036854775808", "bytes */10", "", 416},
	} {
		t.Run(tc.name, func(t *testing.T) {
			steps, err := RangeAsset("https://download.itunes.apple.com/asset", []byte("0123456789"), 1)
			if err != nil {
				t.Fatal(err)
			}
			s := newFixture(t, InMemory, steps...)
			r, _ := http.NewRequest("GET", steps[0].Url, nil)
			r.Header = steps[0].Header.Clone()
			r.Header.Set("Range", tc.request)
			response, err := s.Client().Do(r)
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(response.Body)
			response.Body.Close()
			if err != nil || response.StatusCode != tc.status || response.Header.Get("Content-Range") != tc.wantRange || string(body) != tc.wantBody || response.ContentLength != int64(len(tc.wantBody)) {
				t.Fatalf("range %q: status=%d header=%q body=%q err=%v", tc.request, response.StatusCode, response.Header.Get("Content-Range"), body, err)
			}
			if err := s.Verify(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRangeAssetRequestBudget(t *testing.T) {
	for _, calls := range []int{0, 1, 2, 3} {
		t.Run(strings.Repeat("request-", calls)+"end", func(t *testing.T) {
			steps, err := RangeAsset("https://download.itunes.apple.com/asset", []byte("123"), 2)
			if err != nil {
				t.Fatal(err)
			}
			s := newFixture(t, InMemory, steps...)
			for range calls {
				r, _ := http.NewRequest("GET", steps[0].Url, nil)
				r.Header = steps[0].Header.Clone()
				r.Header.Set("Range", "bytes=-1")
				response, _ := s.Client().Do(r)
				if response != nil {
					response.Body.Close()
				}
			}
			err = s.Verify()
			if (err == nil) != (calls == 1 || calls == 2) {
				t.Fatal("range request budget not enforced", calls, err)
			}
		})
	}
}

func TestOptionalStepsMustBeTrailing(t *testing.T) {
	_, err := New(InMemory,
		Step{Method: "GET", Url: "https://gsa.apple.com/optional", Optional: true},
		Step{Method: "GET", Url: "https://gsa.apple.com/required"},
	)
	if err == nil {
		t.Fatal("optional step could hide a later required stage")
	}
}
