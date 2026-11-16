package debuglog

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http/httputil"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"appleseum/backend/internal/fault"
	"appleseum/backend/internal/wire"

	"github.com/andybalholm/brotli"
	"github.com/google/uuid"
	"github.com/hyperion-cs/appleseum/applekit"
	"github.com/klauspost/compress/zstd"
)

type testHarBody struct {
	Text, Encoding, MimeType, Comment string
	RequestEncoding                   string `json:"_encoding"`
	Size                              int64
	Compression                       *int64
}
type testHarMessage struct {
	Method, Url, HttpVersion, RedirectUrl, Comment string
	Status                                         int
	Headers, QueryString                           []harPair
	Cookies                                        []harCookie
	BodySize                                       int64
	PostData                                       *testHarBody
	Content                                        testHarBody
}
type testHarEntry struct {
	StartedDateTime   string
	Time              float64
	Request, Response testHarMessage
	Comment           string
	Timings           struct{ Comment string }
	FileModified      string `json:"_fileModified"`
}

func debugGzip(t *testing.T, data []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	w := gzip.NewWriter(&out)
	w.Write(data)
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func debugMessage(line, headers string, body []byte) []byte {
	return append([]byte(fmt.Sprintf("%s\r\n%sContent-Length: %d\r\n\r\n", line, headers, len(body))), body...)
}

func readTestHar(t *testing.T, data []byte) []testHarEntry {
	t.Helper()
	var archive struct {
		Log struct {
			Version string
			Entries []testHarEntry
		}
	}
	if err := json.Unmarshal(data, &archive); err != nil || archive.Log.Version != "1.2" || len(archive.Log.Entries) == 0 {
		t.Fatal("invalid HAR", err, string(data))
	}
	return archive.Log.Entries
}

func harFixture(t *testing.T, request, response []byte, source string) (debugHarFile, []byte) {
	t.Helper()
	exchange := append(append(bytes.Clone(request), debugResponseSeparator...), response...)
	path := filepath.Join(t.TempDir(), "exchange-"+uuid.Must(uuid.NewV7()).String()+".http")
	if err := os.WriteFile(path, exchange, 0600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return debugHarFile{file: f, event: wire.DebugEvent{Id: 1, Source: source, At: time.Now().UTC().Format(time.RFC3339Nano)}}, exchange
}

func TestDebugHarContentDecoding(t *testing.T) {
	plain := []byte(`{"message":"Текст 🌍", "quote":"\\\"", "marker":"` + debugResponseSeparator + `"}`)

	compress := func(kind string, data []byte) []byte {
		var out bytes.Buffer
		var w io.WriteCloser
		switch kind {
		case "gzip":
			return debugGzip(t, data)
		case "deflate":
			w = zlib.NewWriter(&out)
		case "raw deflate":
			w, _ = flate.NewWriter(&out, 1)
		case "br":
			w = brotli.NewWriter(&out)
		case "zstd":
			w, _ = zstd.NewWriter(&out, zstd.WithEncoderConcurrency(1))
		}
		w.Write(data)
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		return out.Bytes()
	}

	for _, kind := range []string{"", "gzip", "deflate", "raw deflate", "br", "zstd", "gzip, br"} {
		t.Run(kind, func(t *testing.T) {
			body := plain
			encoding := kind
			if kind == "raw deflate" {
				encoding = "deflate"
			}
			if kind == "gzip, br" {
				body = compress("br", compress("gzip", plain))
			} else if kind != "" {
				body = compress(kind, plain)
			}
			headers := "Content-Type: application/json; charset=utf-8\r\nContent-Encoding: " + encoding + "\r\n"
			input, exchange := harFixture(t, debugMessage("POST https://example.test/action?q=one&q=two HTTP/1.1", headers+"Cookie: first=one; second=two\r\n", body), debugMessage("HTTP/2.0 200 OK", headers+"Set-Cookie: token=first; HttpOnly; Secure; Path=/\r\nSet-Cookie: token=second; Path=/other\r\n", body), "Apple")
			var out bytes.Buffer
			if err := writeDebugHar(context.Background(), &out, []debugHarFile{input}); err != nil {
				t.Fatal(err)
			}
			entry := readTestHar(t, out.Bytes())[0]
			if entry.Request.PostData == nil || entry.Request.PostData.Text != string(plain) || entry.Response.Content.Text != string(plain) || entry.Response.Content.Encoding != "" {
				t.Fatal("decoded bodies not readable", out.String())
			}
			if entry.Request.Url != "https://example.test/action?q=one&q=two" || entry.Request.HttpVersion != "HTTP/2.0" || !reflect.DeepEqual(entry.Request.QueryString, []harPair{{"q", "one"}, {"q", "two"}}) || len(entry.Request.Cookies) != 2 || len(entry.Response.Cookies) != 2 || len(entry.Response.Headers) < 3 {
				t.Fatal("URL, duplicate query, headers or cookies lost")
			}
			if entry.Response.BodySize != int64(len(body)) || entry.Response.Content.Size != int64(len(plain)) {
				t.Fatal("exchange/decoded sizes differ")
			}
			if kind != "" && (entry.Response.Content.Compression == nil || *entry.Response.Content.Compression != int64(len(plain)-len(body))) {
				t.Fatal("decompression metadata missing")
			}
			if entry.Request.PostData.Comment != "" || entry.Response.Content.Comment != "" {
				t.Fatal("successful decoding added an unnecessary comment")
			}
			data, _ := os.ReadFile(input.file.Name())
			if !bytes.Equal(data, exchange) {
				t.Fatal("HAR rewrote original capture")
			}
			if files, _ := filepath.Glob(filepath.Join(filepath.Dir(input.file.Name()), ".har-body-*")); len(files) != 0 {
				t.Fatal("temporary body files leaked")
			}
		})
	}
}

func TestDebugHarReadableXml(t *testing.T) {
	body := []byte("<message key=\"value\">text &amp; more</message>\n\\path\t🌍")
	headers := "Content-Type: application/xml\r\nX-Test: <value>&\r\n"
	input, exchange := harFixture(t, debugMessage("POST https://example.test/?a=1&b=2 HTTP/1.1", headers, body), debugMessage("HTTP/1.1 200 OK", headers, body), "Apple")
	var out bytes.Buffer
	if err := writeDebugHar(context.Background(), &out, []debugHarFile{input, input}); err != nil {
		t.Fatal(err)
	}
	for _, escape := range []string{`\u003c`, `\u003e`, `\u0026`} {
		if bytes.Contains(out.Bytes(), []byte(escape)) {
			t.Fatalf("unnecessary HTML escape %s in HAR", escape)
		}
	}
	entries := readTestHar(t, out.Bytes())
	if len(entries) != 2 {
		t.Fatal("batch entries lost")
	}
	for _, entry := range entries {
		if entry.Timings.Comment != "Timing phases are estimates." {
			t.Fatal("estimated timings are not identified")
		}
		if entry.Request.PostData == nil || entry.Request.PostData.Text != string(body) || entry.Response.Content.Text != string(body) {
			t.Fatal("JSON escaping changed the XML body")
		}
		if entry.Request.Url != "https://example.test/?a=1&b=2" {
			t.Fatal("URL changed")
		}
		for _, message := range []testHarMessage{entry.Request, entry.Response} {
			if !slices.Contains(message.Headers, harPair{"X-Test", "<value>&"}) {
				t.Fatal("header changed")
			}
		}
	}
	original, err := os.ReadFile(input.file.Name())
	if err != nil || !bytes.Equal(original, exchange) {
		t.Fatal("export changed the original capture", err)
	}
}

func TestDebugHarPrettyJson(t *testing.T) {
	plain := []byte(`{"number":9007199254740993,"exponent":1.2300e+42,"same":1,"same":2,"items":[true,null,{"text":"Текст 🌍\n\"quoted\""}]}`)
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, plain, "", "  "); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, contentType string
		body, want        []byte
	}{
		{"JSON", "application/json", plain, pretty.Bytes()},
		{"JSON suffix", "application/problem+json; charset=utf-8", plain, pretty.Bytes()},
		{"incomplete JSON", "application/json", []byte(`{"partial":`), []byte(`{"partial":`)},
		{"plain text", "text/plain", plain, plain},
		{"large JSON", "application/json", []byte(`{"large":"` + strings.Repeat("x", maxHarPrettyBody) + `"}`), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.want == nil {
				tc.want = tc.body
			}
			headers := "Content-Type: " + tc.contentType + "\r\nContent-Encoding: gzip\r\n"
			compressed := debugGzip(t, tc.body)
			input, exchange := harFixture(t, debugMessage("POST https://example.test/ HTTP/1.1", headers, compressed), debugMessage("HTTP/1.1 200 OK", headers, compressed), "Apple")
			var out bytes.Buffer
			if err := writeDebugHar(context.Background(), &out, []debugHarFile{input, input}); err != nil {
				t.Fatal(err)
			}
			var canonical bytes.Buffer
			if err := json.Indent(&canonical, bytes.TrimSpace(out.Bytes()), "", "  "); err != nil || !bytes.Equal(bytes.TrimSpace(out.Bytes()), canonical.Bytes()) {
				t.Fatal("HAR itself is not indented", err)
			}
			entries := readTestHar(t, out.Bytes())
			if len(entries) != 2 {
				t.Fatal("batch entries lost")
			}
			for _, entry := range entries {
				if entry.Request.PostData == nil || entry.Request.PostData.Text != string(tc.want) || entry.Response.Content.Text != string(tc.want) {
					t.Fatal("request/response JSON formatting changed values or was skipped")
				}
				if entry.Response.Content.Comment != "" || entry.Request.PostData.Comment != "" {
					t.Fatal("formatting added an unnecessary comment")
				}
				if entry.Response.Content.Size != int64(len(tc.body)) || entry.Response.BodySize != int64(len(compressed)) || entry.Request.BodySize != int64(len(compressed)) {
					t.Fatal("original byte sizes changed by formatting")
				}
			}
			original, _ := os.ReadFile(input.file.Name())
			if !bytes.Equal(original, exchange) {
				t.Fatal("pretty export rewrote original capture")
			}
		})
	}
}

func TestDebugHarChunkingBinaryAndFailures(t *testing.T) {
	plain := []byte("readable reply")
	compressed := debugGzip(t, plain)
	var chunks bytes.Buffer
	writer := httputil.NewChunkedWriter(&chunks)
	writer.Write(compressed[:10])
	writer.Write(compressed[10:])
	writer.Close()
	chunks.WriteString("X-Summary: trailer-marker\r\n\r\n")
	corrupt := bytes.Clone(compressed)
	corrupt[len(corrupt)-8] ^= 1
	for _, tc := range []struct {
		name, source, headers string
		body, want            []byte
		binary, warning       bool
	}{
		{"chunks", "API", "Content-Encoding: gzip\r\nTransfer-Encoding: chunked\r\n", chunks.Bytes(), plain, false, false},
		{"invalid chunks", "API", "Transfer-Encoding: chunked\r\n", []byte("invalid chunks"), []byte("invalid chunks"), true, true},
		{"dechunked Apple", "Apple", "Content-Encoding: gzip\r\nTransfer-Encoding: chunked\r\n", compressed, plain, false, false},
		{"binary", "API", "Content-Type: application/octet-stream\r\n", []byte{0, 1, 2, 255}, []byte{0, 1, 2, 255}, true, false},
		{"truncated", "Apple", "Content-Encoding: gzip\r\n", compressed[:len(compressed)-5], compressed[:len(compressed)-5], true, true},
		{"checksum", "Apple", "Content-Encoding: gzip\r\n", corrupt, corrupt, true, true},
		{"unsupported", "Apple", "Content-Encoding: unknown\r\n", compressed, compressed, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := debugMessage("GET http://example.test/ HTTP/1.1", "", nil)
			response := append([]byte("HTTP/1.1 100 Continue\r\n\r\nHTTP/1.1 200 OK\r\n"+tc.headers+"\r\n"), tc.body...)
			input, _ := harFixture(t, request, response, tc.source)
			var out bytes.Buffer
			if err := writeDebugHar(context.Background(), &out, []debugHarFile{input}); err != nil {
				t.Fatal(err)
			}
			content := readTestHar(t, out.Bytes())[0].Response.Content
			body := []byte(content.Text)
			if content.Encoding == "base64" {
				var err error
				body, err = base64.StdEncoding.DecodeString(content.Text)
				if err != nil {
					t.Fatal(err)
				}
			}
			if !bytes.Equal(body, tc.want) || (content.Encoding == "base64") != tc.binary || strings.Contains(content.Comment, "Could not ") != tc.warning {
				t.Fatal("body was corrupted or decode failure hidden", out.String())
			}
			if tc.name == "chunks" && !bytes.Contains(out.Bytes(), []byte("trailer-marker")) {
				t.Fatal("trailers lost")
			}
			if !bytes.Contains(out.Bytes(), []byte("_informationalResponses")) {
				t.Fatal("interim headers lost")
			}
		})
	}
	// Encoding/json must not replace invalid UTF-8 or split multibyte runes.
	for _, body := range [][]byte{bytes.Repeat([]byte("я🌍\n\""), 20_000), {255, 254, 0}} {
		input, _ := harFixture(t, debugMessage("POST https://example.test/ HTTP/1.1", "Content-Type: text/plain\r\n", body), debugMessage("HTTP/1.1 200 OK", "Content-Type: text/plain\r\n", body), "Apple")
		var out bytes.Buffer
		if err := writeDebugHar(context.Background(), &out, []debugHarFile{input}); err != nil {
			t.Fatal(err)
		}
		entry := readTestHar(t, out.Bytes())[0]
		result := []byte(entry.Response.Content.Text)
		if entry.Response.Content.Encoding == "base64" {
			result, _ = base64.StdEncoding.DecodeString(entry.Response.Content.Text)
			if entry.Request.PostData.RequestEncoding != "base64" {
				t.Fatal("binary request extension missing")
			}
		}
		if !bytes.Equal(result, body) {
			t.Fatal("UTF-8/binary round trip changed bytes")
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := writeDebugHar(ctx, io.Discard, []debugHarFile{input}); !errors.Is(err, context.Canceled) {
			t.Fatal("cancellation ignored", err)
		}
	}
}

func TestDebugHarDownloadsPreserveDatesAndOriginals(t *testing.T) {
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, "packages"), 0700)
	app, err := New(Config{Enabled: true, Directory: filepath.Join(dir, "logs")})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	exchange := append(debugMessage("GET https://example.test/ HTTP/1.1", "", nil), debugResponseSeparator...)
	exchange = append(exchange, debugMessage("HTTP/2.0 200 OK", "Content-Encoding: gzip\r\n", debugGzip(t, []byte("readable response")))...)
	save := func() string {
		app.ObserveApple(applekit.RequestEvent{Status: 200, ResponseBody: exchange, ResponseBytes: int64(len(exchange))})
		e, _ := app.log.find(app.log.next)
		return filepath.Join(app.log.directory, e.file)
	}
	path := save()
	modified := time.Date(2_025, 5, 6, 7, 8, 9, 0, time.UTC)
	if err := os.Chtimes(path, modified, modified); err != nil {
		t.Fatal(err)
	}

	har := func(ids ...int64) (time.Time, []testHarEntry) {
		t.Helper()
		exchanges, err := app.OpenExchanges(ids)
		if err != nil {
			t.Fatal(err)
		}
		defer exchanges.Close()
		var out bytes.Buffer
		if err = exchanges.WriteHar(context.Background(), &out); err != nil {
			t.Fatal(err)
		}
		return exchanges.Modified, readTestHar(t, out.Bytes())
	}
	if at, entries := har(1); !at.Equal(modified) || entries[0].FileModified != modified.Format(time.RFC3339Nano) || entries[0].Response.Content.Text != "readable response" {
		t.Fatal("entry date or body differs")
	}
	second := save()
	newer := modified.Add(time.Hour)
	if err := os.Chtimes(second, newer, newer); err != nil {
		t.Fatal(err)
	}
	at, entries := har(1, 2)
	if len(entries) != 2 || entries[0].FileModified != modified.Format(time.RFC3339Nano) || entries[1].FileModified != newer.Format(time.RFC3339Nano) || !at.Equal(newer) {
		t.Fatal("batch export replaced individual modification times")
	}
	exchanges, err := app.OpenExchanges([]int64{1})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(exchanges.Raw())
	exchanges.Close()
	if !bytes.Equal(raw, exchange) {
		t.Fatal("raw access changed")
	}
	if _, err := app.OpenExchanges([]int64{1, 3}); !errors.Is(err, fault.NotFound) {
		t.Fatal("missing exchange accepted", err)
	}
	data, _ := os.ReadFile(path)
	info, _ := os.Stat(path)
	if !bytes.Equal(data, exchange) || !info.ModTime().Equal(modified) {
		t.Fatal("export modified source")
	}
	csv, csvModified := app.Csv()
	if csvModified.IsZero() || !strings.HasPrefix(string(csv), strings.Join(csvHeader, ",")) {
		t.Fatal("CSV export regressed")
	}
}

func TestDebugHarLegacyCharsetAndStreamingErrors(t *testing.T) {
	for _, tc := range []struct {
		name, contentType string
		data              []byte
		want              string
		encoding          string
	}{
		{"XML plist", "application/x-apple-plist", []byte(`<?xml version="1.0"?><plist><string>readable</string></plist>`), `<?xml version="1.0"?><plist><string>readable</string></plist>`, ""},
		{"Latin-1", "text/plain; charset=iso-8859-1", []byte{'c', 'a', 'f', 233}, "café", ""},
		{"unknown charset", "text/plain; charset=unknown-test-charset", []byte("x"), "eA==", "base64"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input, _ := harFixture(t, debugMessage("GET /old?x=one HTTP/1.1", "Host: example.test\r\n", nil), debugMessage("HTTP/1.1 200 OK", "Content-Type: "+tc.contentType+"\r\n", tc.data), "Apple")
			var out bytes.Buffer
			if err := writeDebugHar(context.Background(), &out, []debugHarFile{input}); err != nil {
				t.Fatal(err)
			}
			entry := readTestHar(t, out.Bytes())[0]
			if entry.Request.Url != "https://example.test/old?x=one" || entry.Response.Content.Text != tc.want || entry.Response.Content.Encoding != tc.encoding {
				t.Fatal("legacy URL/text lost", out.String())
			}
			if entry.Request.Comment != "URL scheme inferred from capture source and port." || (entry.Response.Content.Comment != "") != (tc.encoding == "base64") {
				t.Fatal("missing limitation or unnecessary success comment")
			}
			if err := writeDebugHar(context.Background(), debugHarFailure{}, []debugHarFile{input}); !errors.Is(err, io.ErrClosedPipe) {
				t.Fatal("writer error hidden", err)
			}
		})
	}
	input, _ := harFixture(t, []byte("legacy response-only bytes"), nil, "Apple")
	input.event.Detail = "transport failed"
	var out bytes.Buffer
	if err := writeDebugHar(context.Background(), &out, []debugHarFile{input}); err != nil {
		t.Fatal(err)
	}
	entry := readTestHar(t, out.Bytes())[0]
	if entry.Request.Url != "about:blank" || entry.Comment != "transport failed\nRequest headers could not be parsed; URL unknown." || entry.Response.Content.Encoding != "base64" {
		t.Fatal("legacy bytes silently discarded")
	}
	body := newHarBody(input.file, 0, 10)
	defer body.close()
	if err := body.spool(context.Background(), strings.NewReader("a body longer than the limit"), 8); err == nil || body.file != input.file || body.size != 10 {
		t.Fatal("oversized decoded body replaced source")
	}
}

type debugHarFailure struct{}

func (debugHarFailure) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
