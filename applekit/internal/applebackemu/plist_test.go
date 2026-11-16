package applebackemu

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestPlistRequestPreservesTypes(t *testing.T) {
	const valid = `<plist version="1.0"><dict><key>enabled</key><true/><key>payload</key><data>AQI=</data></dict></plist>`
	for _, tc := range []struct {
		name, body string
		valid      bool
	}{
		{"valid", valid, true},
		{"data-as-string", strings.ReplaceAll(valid, "<data>AQI=</data>", "<string>AQI=</string>"), false},
		{"boolean-as-integer", strings.ReplaceAll(valid, "<true/>", "<integer>1</integer>"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			step, err := PlistRequest("https://gsa.apple.com/test", map[string]any{"payload": []byte{1, 2}, "enabled": true}, nil, Response{})
			if err != nil {
				t.Fatal(err)
			}
			s := newFixture(t, InMemory, step)
			response, err := s.Client().Post(step.Url, "text/x-xml-plist", strings.NewReader(tc.body))
			if response != nil {
				response.Body.Close()
			}
			if tc.valid {
				if err != nil || s.Verify() != nil {
					t.Fatal("valid plist rejected", err)
				}
			} else if err == nil || s.Verify() == nil {
				t.Fatal("plist type mismatch accepted")
			}
		})
	}
}

func TestGzipResponseAcceptsMissingHeadersAndUpdatesLength(t *testing.T) {
	for _, header := range []http.Header{nil, {"X-Fixture": {"original"}}} {
		length := int64(9)
		original := Response{Header: header, Body: []byte("synthetic"), ContentLength: &length}
		response, err := GzipResponse(original)
		if err != nil {
			t.Fatal(err)
		}
		reader, err := gzip.NewReader(bytes.NewReader(response.Body))
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(reader)
		reader.Close()
		if err != nil || string(data) != "synthetic" || response.Header.Get("Content-Encoding") != "gzip" || response.ContentLength != nil {
			t.Fatal("compressed response is invalid", err)
		}
		if header.Get("Content-Encoding") != "" || *original.ContentLength != 9 {
			t.Fatal("gzip helper changed the original response")
		}
	}
}
