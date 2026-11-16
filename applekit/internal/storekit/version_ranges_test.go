package storekit

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hyperion-cs/appleseum/applekit/internal/applebackemu"
	"github.com/hyperion-cs/appleseum/applekit/internal/httpx"
)

const (
	versionTestPlist = `<plist><dict><key>CFBundleShortVersionString</key><string>2.4.1</string><key>CFBundleVersion</key><string>81</string><key>MinimumOSVersion</key><string>16.2</string></dict></plist>`
)

func rangeIpa(t *testing.T, method uint16, before string, entries int) []byte {
	t.Helper()
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)

	add := func(name, body string) {
		h := &zip.FileHeader{Name: name, Method: method}
		w, err := z.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}

	if before != "" {
		add("resource", before)
	}
	add("Payload/Test.app/PlugIns/Nested.app/Info.plist", `<plist><dict><key>CFBundleVersion</key><string>99</string></dict></plist>`)
	add("Payload/Test.app/Info.plist", versionTestPlist)
	// A large directory must not increase the request count.
	for i := 0; i < entries; i++ {
		add(strings.Repeat("r", 150), "")
	}
	add("Payload/Test.app/data", strings.Repeat("x", 2_097_152))
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestVersionInfoThreeRangesRegardlessOfEntryOrder(t *testing.T) {
	for _, before := range []string{"", strings.Repeat("r", 300_000)} {
		t.Run(fmt.Sprintf("preceding_bytes=%d", len(before)), func(t *testing.T) {
			raw := rangeIpa(t, zip.Store, before, 15_000)
			steps, err := applebackemu.RangeAsset(fixtureAssetUrl, raw, 3)
			if err != nil {
				t.Fatal(err)
			}
			var transferred atomic.Int64
			for i := range steps {
				respond := steps[i].Respond

				steps[i].Respond = func(req applebackemu.Request) (applebackemu.Response, error) {
					res, err := respond(req)
					transferred.Add(int64(len(res.Body)))
					return res, err
				}
			}
			session, backend := downloadBackend(t, applebackemu.InMemory, downloadAccount(), fixtureDispatchUrl, steps...)
			var percentages []float64
			var requestsAtStage []int
			got, err := readRemoteVersion(context.Background(), session.Client, fixtureAssetUrl, func(percent float64) {
				percentages = append(percentages, percent)
				requestsAtStage = append(requestsAtStage, len(backend.Requests()))
			})
			if err != nil {
				t.Fatal(err)
			}
			if got != (VersionMetadata{Version: "2.4.1", Build: "81", MinimumOs: "16.2"}) || len(backend.Requests()) != 3 {
				t.Fatalf("metadata=%+v requests=%d", got, len(backend.Requests()))
			}
			if transferred.Load() > 3_500_000 {
				t.Fatalf("excessive transfer: %d", transferred.Load())
			}
			if !slices.Equal(percentages, []float64{100.0 / 3, 200.0 / 3, 100}) || !slices.Equal(requestsAtStage, []int{1, 2, 3}) {
				t.Fatalf("progress did not follow completed ranges: percentages=%v requests=%v", percentages, requestsAtStage)
			}
		})
	}
}

func TestVersionRangesShareHttpsConnectionAcrossVersions(t *testing.T) {
	raw := rangeIpa(t, zip.Deflate, "resource", 15_000)
	for _, h2 := range []bool{false, true} {
		t.Run(fmt.Sprintf("HTTP2=%t", h2), func(t *testing.T) {
			var requests, connections, handshakes, reused atomic.Int32
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				http.ServeContent(w, r, "asset", time.Time{}, bytes.NewReader(raw))
			}))
			server.EnableHTTP2 = h2

			server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
				if state == http.StateNew {
					connections.Add(1)
				}
			}

			server.StartTLS()
			defer server.Close()
			roots := x509.NewCertPool()
			roots.AddCert(server.Certificate())
			client := httpx.NewClientWithOptions(nil, httpx.ClientOptions{RootCas: roots})
			defer client.CloseIdleConnections()
			for version := range 2 {
				ctx, cancel := context.WithCancel(httpx.WithProfile(context.Background(), httpx.Configurator))
				ctx = httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{
					TLSHandshakeDone: func(_ tls.ConnectionState, err error) {
						if err == nil {
							handshakes.Add(1)
						}
					},
					GotConn: func(info httptrace.GotConnInfo) {
						if info.Reused {
							reused.Add(1)
						}
					},
				})
				// VersionInfo constructs a client per version but shares the session's
				// transport and jar. Cancelling a finished version must preserve its pool.
				perVersion := &http.Client{Transport: client.Transport, Jar: client.Jar}
				got, err := readRemoteVersion(ctx, perVersion, fmt.Sprintf("%s/%d.ipa", server.URL, version), nil)
				cancel()
				if err != nil || got.Version != "2.4.1" {
					t.Fatalf("version=%+v error=%v", got, err)
				}
			}
			if requests.Load() != 6 || connections.Load() != 1 || handshakes.Load() != 1 || reused.Load() != 5 {
				t.Fatalf("requests=%d connections=%d handshakes=%d reused=%d", requests.Load(), connections.Load(), handshakes.Load(), reused.Load())
			}
		})
	}
}

func TestVersionDirectoryBudgetBeforeRequest(t *testing.T) {
	r := &ipaRangeReader{size: 1_073_741_824, remaining: 16_777_216, directory: true}
	if _, err := r.ReadAt(make([]byte, 46), 1_048_576); err == nil {
		t.Fatal("directory exceeded the range budget")
	}
}

func TestVersionInfoMaximumLocalHeaderStillUsesThreeRanges(t *testing.T) {
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	w, err := z.CreateHeader(&zip.FileHeader{Name: "before", Method: zip.Store})
	if err != nil {
		t.Fatal(err)
	}
	w.Write([]byte(strings.Repeat("x", 1_048_576)))
	w, err = z.CreateHeader(&zip.FileHeader{
		Name:   "Payload/" + strings.Repeat("a", 65_500) + ".app/Info.plist",
		Method: zip.Deflate,
		Extra:  make([]byte, 65_535),
	})
	if err != nil {
		t.Fatal(err)
	}
	w.Write([]byte(versionTestPlist))
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	steps, err := applebackemu.RangeAsset(fixtureAssetUrl, buf.Bytes(), 3)
	if err != nil {
		t.Fatal(err)
	}
	session, backend := downloadBackend(t, applebackemu.InMemory, downloadAccount(), fixtureDispatchUrl, steps...)
	got, err := readRemoteVersion(context.Background(), session.Client, fixtureAssetUrl, nil)
	if err != nil || got.Version != "2.4.1" || len(backend.Requests()) != 3 {
		t.Fatalf("metadata=%+v requests=%d error=%v", got, len(backend.Requests()), err)
	}
}

func TestVersionInfoRejectsOversizedPlist(t *testing.T) {
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	w, _ := z.Create("Payload/Test.app/Info.plist")
	w.Write([]byte(strings.Repeat("x", (1_048_576)+1)))
	z.Close()
	steps, err := applebackemu.RangeAsset(fixtureAssetUrl, buf.Bytes(), 2)
	if err != nil {
		t.Fatal(err)
	}
	session, _ := downloadBackend(t, applebackemu.InMemory, downloadAccount(), fixtureDispatchUrl, steps...)
	if _, err := readRemoteVersion(context.Background(), session.Client, fixtureAssetUrl, nil); err == nil {
		t.Fatal("oversized plist accepted")
	}
}
