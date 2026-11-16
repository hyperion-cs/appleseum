package storekit

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/md5"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"howett.net/plist"

	"github.com/hyperion-cs/appleseum/applekit/internal/ams"
	"github.com/hyperion-cs/appleseum/applekit/internal/applebackemu"
	"github.com/hyperion-cs/appleseum/applekit/internal/httpx"
)

func TestDownloadLatestReusesCompleteVersionResponse(t *testing.T) {
	for _, tc := range []struct {
		name, api, change, wantError string
		posts                        int
		wantRejection                bool
		mode                         applebackemu.Mode
	}{
		{name: "plain", posts: 1},
		{name: "https", posts: 1, mode: applebackemu.HTTPS},
		{name: "byte progress", api: "bytes", posts: 1},
		{name: "tracked progress", api: "tracked", posts: 1},
		{name: "missing license", change: "license", posts: 2},
		{name: "empty license", change: "empty license", posts: 2},
		{name: "missing URL", change: "URL", posts: 2},
		{name: "inferred version", change: "version", posts: 2},
		{name: "missing metadata", change: "metadata", posts: 1, wantError: "no metadata"},
		{name: "missing versions", change: "versions", posts: 1, wantError: "no version identifiers"},
		{name: "bad checksum", change: "checksum", posts: 1, wantError: "md5 mismatch"},
		{name: "fallback rejected", change: "rejected", posts: 2, wantRejection: true},
		{name: "explicit version", api: "explicit", posts: 1},
		{name: "list versions", api: "versions", posts: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source, payload := rawSource(t, false)
			archive, err := os.ReadFile(source)
			if err != nil {
				t.Fatal(err)
			}
			account := downloadAccount()
			version := "7"
			if tc.api == "explicit" {
				version = "9"
			}
			steps := make([]applebackemu.Step, 0, tc.posts+1)
			for post := 1; post <= tc.posts; post++ {
				license := "first-license"
				if post > 1 {
					license = "explicit-license"
				}
				item := map[string]any{
					"URL": fixtureAssetUrl, "md5": fmt.Sprintf("%x", md5.Sum(archive)),
					"sinfs":    []any{map[string]any{"sinf": []byte(license)}},
					"metadata": map[string]any{"softwareVersionExternalIdentifier": version, "softwareVersionExternalIdentifiers": []any{"5", "7"}},
				}
				if post == 1 {
					switch tc.change {
					case "license", "rejected":
						delete(item, "sinfs")
					case "empty license":
						item["sinfs"] = []any{map[string]any{"sinf": []byte{}}}
					case "URL":
						delete(item, "URL")
					case "version":
						delete(item["metadata"].(map[string]any), "softwareVersionExternalIdentifier")
					case "metadata":
						delete(item, "metadata")
					case "versions":
						item["metadata"] = map[string]any{}
					case "checksum":
						item["md5"] = strings.Repeat("0", 32)
					}
				}
				response := downloadReply(t, map[string]any{"songList": []any{item}})
				response.Header.Set("Set-Cookie", "ticket=issued; Domain=.itunes.apple.com; Path=/; Secure")
				if tc.change == "rejected" && post == 2 {
					response.StatusCode = http.StatusUnauthorized
				}
				requested := ""
				if post > 1 || tc.api == "explicit" {
					requested = version
				}
				step := modernDownloadStep(dispatchStep(t, account, fixtureDispatchUrl, 1_234, "", requested, response), "en-us")
				step.Header.Set("X-Apple-AMD", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 24)))
				step.Header.Set("X-Apple-AMD-M", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{2}, 60)))
				if post > 1 {
					step.Header.Set("Cookie", "ticket=issued")
				} else {
					step.Header["Cookie"] = nil
				}
				steps = append(steps, step)
			}
			if tc.api != "versions" && !tc.wantRejection && (tc.wantError == "" || tc.change == "checksum") {
				step := applebackemu.DownloadAsset(fixtureAssetUrl, applebackemu.Response{Body: archive})
				step.Header.Set("Cookie", "ticket=issued")
				steps = append(steps, step)
			}
			s, backend := downloadBackend(t, tc.mode, account, fixtureDispatchUrl, steps...)
			if err := s.ConfigureAmd(&ams.AmdController{Provider: &downloadAmd{ready: true}, Bag: ams.Bag{}}, ams.RequestContext{Language: "en-us"}); err != nil {
				t.Fatal(err)
			}
			profiles := observeDownloadProfiles(s)

			counts := func() (int, int) {
				posts, assets := 0, 0
				kb := ""
				for _, request := range backend.Requests() {
					if request.Method == "GET" {
						assets++
						continue
					}
					posts++
					var payload map[string]any
					if _, err := plist.Unmarshal(request.Body, &payload); err != nil {
						t.Fatal(err)
					}
					value, _ := payload["kbsync"].(string)
					if kb == "" {
						kb = value
					}
					if value == "" || kb != value {
						t.Fatal("dispatch changed kbsync across version requests")
					}
				}
				profiles.check(t, posts+assets)
				return posts, assets
			}

			out := filepath.Join(t.TempDir(), "result.ipa")
			var received, total int64
			prepared := false
			ctx := httpx.WithProfile(context.Background(), httpx.Accounts)
			switch tc.api {
			case "bytes":
				_, err = s.Download(ctx, 1_234, "", out, func(p DownloadProgress) {
					if p.Stage == "downloading" {
						received, total = p.Received, p.Total
					}
				})
			case "tracked":
				_, err = s.Download(ctx, 1_234, "", out, func(p DownloadProgress) {
					if p.Stage == "preparing" {
						prepared = true
					} else {
						received, total = p.Received, p.Total
					}
				})
			case "explicit":
				_, err = s.Download(ctx, 1_234, version, out, nil)
			case "versions":
				var ids []string
				var latest string
				ids, latest, err = s.ListVersions(ctx, 1_234)
				posts, assets := counts()
				if err != nil || strings.Join(ids, ",") != "5,7" || latest != "7" || assets != 0 || posts != 1 {
					t.Fatal("list versions changed", ids, latest, err)
				}
				return
			default:
				_, err = s.Download(ctx, 1_234, "", out, nil)
			}
			posts, assets := counts()
			if posts != tc.posts {
				t.Fatal("unexpected dispatch count", posts)
			}
			if tc.wantError != "" || tc.wantRejection {
				if tc.wantRejection {
					var rejected *SessionRejection
					if !errors.As(err, &rejected) || !errors.Is(err, ErrSessionExpired) || rejected.Operation != "download" || rejected.Reason != "authentication" || rejected.Status != http.StatusUnauthorized {
						t.Fatal("expected typed download authentication rejection", err)
					}
				} else if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatal("expected download error", tc.wantError, err)
				}
				if _, statErr := os.Stat(out); !os.IsNotExist(statErr) {
					t.Fatal("failed download produced a final IPA")
				}
				if _, statErr := os.Stat(out + ".tmp"); !os.IsNotExist(statErr) {
					t.Fatal("failed download left a temporary file")
				}
				return
			}
			if err != nil || assets != 1 {
				t.Fatal("download failed", err, assets)
			}
			if (tc.api == "bytes" || tc.api == "tracked") && (received != int64(len(archive)) || total != received) || tc.api == "tracked" && !prepared {
				t.Fatal("download progress changed")
			}
			result, err := zip.OpenReader(out)
			if err != nil {
				t.Fatal(err)
			}
			defer result.Close()
			license := "first-license"
			if tc.posts == 2 {
				license = "explicit-license"
			}
			payload["Payload/Test.app/SC_Info/Test.sinf"] = []byte(license)
			for _, file := range result.File {
				body, err := readZip(file)
				if err != nil {
					t.Fatal("invalid output CRC", err)
				}
				if want, ok := payload[file.Name]; ok {
					if !bytes.Equal(want, body) {
						t.Fatal("payload or selected license changed", file.Name)
					}
					delete(payload, file.Name)
				}
			}
			if len(payload) != 0 {
				t.Fatal("output IPA is incomplete")
			}
		})
	}
}
