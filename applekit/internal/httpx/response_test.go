package httpx

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/andybalholm/brotli"
)

func TestReadResponseBody(t *testing.T) {
	payload := []byte(strings.Repeat("Apple response\n", 100))

	encode := func(encoding string, body []byte) []byte {
		t.Helper()
		var buffer bytes.Buffer
		var writer io.WriteCloser
		switch encoding {
		case "gzip":
			writer = gzip.NewWriter(&buffer)
		case "deflate":
			writer = zlib.NewWriter(&buffer)
		case "br":
			writer = brotli.NewWriter(&buffer)
		default:
			return body
		}
		if _, err := writer.Write(body); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		return buffer.Bytes()
	}

	for _, encoding := range []string{"", "identity", "gzip", "deflate", "br", "gzip, br"} {
		t.Run(encoding, func(t *testing.T) {
			wire := payload
			for _, layer := range strings.Split(encoding, ", ") {
				wire = encode(layer, wire)
			}
			response := &http.Response{Header: http.Header{"Content-Encoding": {encoding}}, Body: io.NopCloser(bytes.NewReader(wire))}
			body, err := ReadResponseBody(response, int64(len(payload)))
			if err != nil || !bytes.Equal(body, payload) {
				t.Fatalf("decode %q: bytes=%d err=%v", encoding, len(body), err)
			}
		})
	}
	for _, tc := range []struct {
		name, encoding string
		wire           []byte
		limit          int64
	}{
		{"wire limit", "", payload, 10},
		{"expanded limit", "gzip", encode("gzip", payload), 100},
		{"wire limit after deflate end", "deflate", append(encode("deflate", []byte("ok")), make([]byte, 1_024)...), 100},
		{"wire limit after brotli end", "br", append(encode("br", []byte("ok")), make([]byte, 1_024)...), 100},
		{"broken gzip", "gzip", []byte("broken"), 100},
		{"broken deflate", "deflate", []byte("broken"), 100},
		{"broken brotli", "br", []byte{0xff, 0xff}, 100},
		{"unknown", "unknown", nil, 100},
		{"invalid limit", "", nil, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := &http.Response{Header: http.Header{"Content-Encoding": {tc.encoding}}, Body: io.NopCloser(bytes.NewReader(tc.wire))}
			if _, err := ReadResponseBody(response, tc.limit); err == nil {
				t.Fatal("invalid response accepted")
			}
		})
	}
}
