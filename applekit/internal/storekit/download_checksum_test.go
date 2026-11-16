package storekit

import (
	"bytes"
	"context"
	"crypto/md5"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hyperion-cs/appleseum/applekit/internal/applebackemu"
)

type checksumBody struct {
	reader io.Reader
	after  func(int, error)
	closes int
}

func (b *checksumBody) Read(p []byte) (int, error) {
	// Exercise hash updates independently of the io.Copy buffer size.
	if len(p) > 1_021 {
		p = p[:1_021]
	}
	n, err := b.reader.Read(p)
	if b.after != nil {
		b.after(n, err)
	}
	return n, err
}

func (b *checksumBody) Close() error { b.closes++; return nil }

type truncatedChecksumReader struct{ io.Reader }

func (r truncatedChecksumReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if err == io.EOF {
		err = io.ErrUnexpectedEOF
	}
	return n, err
}

func TestFetchToFileChecksStreamingMd5AndCleansFailures(t *testing.T) {
	payload := bytes.Repeat([]byte("synthetic IPA bytes\x00"), 4_097)
	for _, tc := range []struct {
		name, mode string
		size       int
	}{
		{name: "empty", size: 0},
		{name: "one byte", size: 1},
		{name: "MD5 block boundary", size: 64},
		{name: "copy buffer boundary", size: 32_769},
		{name: "many reads", size: len(payload)},
		{name: "uppercase", mode: "uppercase", size: len(payload)},
		{name: "no checksum", mode: "none", size: len(payload)},
		{name: "unknown length", mode: "unknown", size: len(payload)},
		{name: "mismatch", mode: "mismatch", size: len(payload)},
		{name: "invalid checksum", mode: "invalid", size: len(payload)},
		{name: "truncated", mode: "truncated", size: len(payload)},
		{name: "canceled during read", mode: "cancel", size: len(payload)},
		{name: "canceled at EOF", mode: "cancel EOF", size: len(payload)},
		{name: "canceled by final progress", mode: "cancel progress", size: len(payload)},
		{name: "destination error", mode: "directory", size: len(payload)},
		{name: "HTTP error", mode: "HTTP", size: len(payload)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := payload[:tc.size]
			expected := fmt.Sprintf("%x", md5.Sum(data))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			body := &checksumBody{reader: bytes.NewReader(data)}
			var wantError error
			wantFailure := false
			switch tc.mode {
			case "uppercase":
				expected = strings.ToUpper(expected)
			case "none":
				expected = ""
			case "mismatch", "invalid", "directory", "HTTP":
				wantFailure = true
				if tc.mode == "invalid" {
					expected = "invalid-md5"
				} else if tc.mode == "mismatch" {
					expected = strings.Repeat("0", 32)
				}
			case "truncated":
				body.reader = truncatedChecksumReader{bytes.NewReader(data[:len(data)/2])}
				wantError = io.ErrUnexpectedEOF
			case "cancel", "cancel EOF", "cancel progress":
				wantError = context.Canceled

				body.after = func(n int, err error) {
					if tc.mode == "cancel" && n > 0 || tc.mode == "cancel EOF" && err == io.EOF {
						cancel()
					}
				}
			}
			length, status := int64(len(data)), http.StatusOK
			if tc.mode == "unknown" {
				length = -1
			}
			if tc.mode == "HTTP" {
				status = http.StatusServiceUnavailable
			}
			response := applebackemu.Response{StatusCode: status, OpenBody: func(context.Context) io.ReadCloser { return body }, ContentLength: &length}
			step := applebackemu.DownloadAsset(fixtureAssetUrl, response)
			s, _ := downloadBackend(t, applebackemu.InMemory, downloadAccount(), fixtureDispatchUrl, step)
			path := filepath.Join(t.TempDir(), "download.tmp")
			if tc.mode == "directory" {
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			}
			var received int64 = -1
			err := s.fetchToFile(ctx, fixtureAssetUrl, path, expected, func(n, total int64) {
				received = n
				if total != length {
					t.Fatal("download total changed")
				}
				if tc.mode == "cancel progress" && n == int64(len(data)) {
					cancel()
				}
			})
			if body.closes != 1 {
				t.Fatal("response body was not closed exactly once", body.closes)
			}
			if wantError != nil || wantFailure {
				if err == nil || wantError != nil && !errors.Is(err, wantError) {
					t.Fatal("expected transfer/checksum failure", wantError, err)
				}
				if tc.mode == "mismatch" || tc.mode == "invalid" {
					if !strings.Contains(err.Error(), "md5 mismatch") {
						t.Fatal("checksum error was lost", err)
					}
				}
				info, statErr := os.Stat(path)
				if tc.mode == "directory" {
					if statErr != nil || !info.IsDir() {
						t.Fatal("failed create removed the existing directory")
					}
				} else if !os.IsNotExist(statErr) {
					t.Fatal("failed download left a partial file", statErr)
				}
				return
			}
			got, readErr := os.ReadFile(path)
			if err != nil || readErr != nil || !bytes.Equal(got, data) || received != int64(len(data)) {
				t.Fatal("successful transfer, checksum or progress differs", err, readErr, received)
			}
		})
	}
}

func TestAssetStreamFailureRemovesPartialFile(t *testing.T) {
	for _, mode := range []struct {
		name string
		mode applebackemu.Mode
	}{{"memory", applebackemu.InMemory}, {"https", applebackemu.HTTPS}} {
		t.Run(mode.name, func(t *testing.T) {
			length := int64(64)
			response := applebackemu.Response{ContentLength: &length, OpenBody: func(context.Context) io.ReadCloser {
				return io.NopCloser(truncatedChecksumReader{bytes.NewReader(bytes.Repeat([]byte{'x'}, 16))})
			}}
			step := applebackemu.DownloadAsset(fixtureAssetUrl, response)
			s, _ := downloadBackend(t, mode.mode, downloadAccount(), fixtureDispatchUrl, step)
			path := filepath.Join(t.TempDir(), "partial.ipa")
			if err := s.fetchToFile(context.Background(), fixtureAssetUrl, path, "", nil); err == nil {
				t.Fatal("interrupted transfer was accepted")
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatal("interrupted transfer retained partial file", err)
			}
		})
	}
}

func BenchmarkDownloadChecksum(b *testing.B) {
	data := bytes.Repeat([]byte("synthetic bytes\x00"), 2_097_152) // 32 MiB, shared by both modes.
	expected := fmt.Sprintf("%x", md5.Sum(data))
	for _, reread := range []bool{false, true} {
		b.Run(fmt.Sprintf("reread=%v", reread), func(b *testing.B) {
			s := &Session{Client: &http.Client{Transport: responseTransport(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(data)), ContentLength: int64(len(data))}, nil
			})}}
			path := filepath.Join(b.TempDir(), "download.tmp")
			b.SetBytes(int64(len(data)))
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				checksum := expected
				if reread {
					checksum = ""
				}
				if err := s.fetchToFile(context.Background(), "https://asset.example.test/ipa", path, checksum, nil); err != nil {
					b.Fatal(err)
				}
				if reread {
					file, err := os.Open(path)
					if err != nil {
						b.Fatal(err)
					}
					h := md5.New()
					_, err = io.Copy(h, file)
					file.Close()
					if err != nil || fmt.Sprintf("%x", h.Sum(nil)) != expected {
						b.Fatal("reread checksum failed", err)
					}
				}
			}
		})
	}
}
