package storekit

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/hyperion-cs/appleseum/applekit/internal/ams"
	"github.com/hyperion-cs/appleseum/applekit/internal/applebackemu"
)

func TestPreparedDownloadsRunConcurrentlyAfterSessionRelease(t *testing.T) {
	source, _ := rawSource(t, false)
	body, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	account := downloadAccount()
	var steps []applebackemu.Step
	for i := 1; i <= 3; i++ {
		item := map[string]any{"URL": fixtureAssetUrl, "sinfs": []any{map[string]any{"sinf": []byte("license")}}, "metadata": map[string]any{"softwareVersionExternalIdentifier": "7", "softwareVersionExternalIdentifiers": []any{"7"}}}
		response := downloadReply(t, map[string]any{"songList": []any{item}})
		response.Header.Set("Set-Cookie", "ticket=issued; Domain=.itunes.apple.com; Path=/; Secure")
		step := modernDownloadStep(dispatchStep(t, account, fixtureDispatchUrl, int64(i), "", "7", response), "en-us")
		step.Header.Set("X-Apple-AMD", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 24)))
		step.Header.Set("X-Apple-AMD-M", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{2}, 60)))
		if i > 1 {
			step.Header.Set("Cookie", "ticket=issued")
		} else {
			step.Header["Cookie"] = nil
		}
		steps = append(steps, step)
	}
	s, _ := downloadBackend(t, applebackemu.InMemory, account, fixtureDispatchUrl, steps...)
	if err := s.ConfigureAmd(&ams.AmdController{Provider: &downloadAmd{ready: true}, Bag: ams.Bag{}}, ams.RequestContext{Language: "en-us"}); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{}, 3)
	gate := make(chan struct{})
	original := s.Client.Transport
	s.Client.Transport = responseTransport(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet {
			return original.RoundTrip(r)
		}
		if r.Header.Get("Cookie") != "ticket=issued" {
			t.Error("prepared transfer lost its private cookie jar")
		}
		started <- struct{}{}
		select {
		case <-gate:
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(body)), ContentLength: int64(len(body)), Request: r}, nil
	})
	var transfers []Download
	for i := 1; i <= 3; i++ {
		transfer, err := s.PrepareDownload(context.Background(), int64(i), "7")
		if err != nil {
			t.Fatal(err)
		}
		transfers = append(transfers, transfer)
	}
	s.ForgetSecrets()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var workers sync.WaitGroup
	for _, transfer := range transfers {
		path := filepath.Join(t.TempDir(), "out.ipa")
		workers.Go(func() {
			if _, err := transfer(ctx, path, nil, DownloadOptions{}); err != nil {
				t.Error(err)
			}
		})
	}
	for range 3 {
		select {
		case <-started:
		case <-ctx.Done():
			close(gate)
			workers.Wait()
			t.Fatal("CDN requests were serialized")
		}
	}
	close(gate)
	workers.Wait()
}

func TestCdnSizeAndIdleLimitsRemovePartialFile(t *testing.T) {
	for _, test := range []struct {
		name    string
		length  int64
		body    io.ReadCloser
		options DownloadOptions
		want    error
	}{
		{"header too large", 100, io.NopCloser(bytes.NewReader([]byte("x"))), DownloadOptions{MaxBytes: 10}, ErrDownloadTooLarge},
		{"unknown length", -1, io.NopCloser(bytes.NewReader(make([]byte, 11))), DownloadOptions{MaxBytes: 10}, ErrDownloadTooLarge},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := &Session{Client: &http.Client{Transport: responseTransport(func(r *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Header: make(http.Header), ContentLength: test.length, Body: test.body, Request: r}, nil
			})}}
			path := filepath.Join(t.TempDir(), "partial")
			err := s.fetchToFileWithOptions(context.Background(), "https://download.itunes.apple.com/file", path, "", test.options, nil)
			if !errors.Is(err, test.want) {
				t.Fatal(err)
			}
			if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("partial file was retained", err)
			}
		})
	}
	t.Run("idle", func(t *testing.T) {
		s := &Session{Client: &http.Client{Transport: responseTransport(func(r *http.Request) (*http.Response, error) { <-r.Context().Done(); return nil, r.Context().Err() })}}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		err := s.fetchToFileWithOptions(ctx, "https://download.itunes.apple.com/file", filepath.Join(t.TempDir(), "partial"), "", DownloadOptions{IdleTimeout: 20 * time.Millisecond}, nil)
		if err == nil || ctx.Err() != nil {
			t.Fatal("idle request ignored its shorter timeout", err)
		}
	})
}
