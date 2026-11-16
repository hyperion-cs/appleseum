package storekit

import (
	"context"
	"net/http"
	"net/url"
	"path/filepath"
	"testing"

	"github.com/hyperion-cs/appleseum/applekit/internal/applebackemu"
)

func TestAssetDownloadSharesAndPersistsSessionCookiesAcrossRedirects(t *testing.T) {
	for _, mode := range []struct {
		name string
		mode applebackemu.Mode
	}{{"memory", applebackemu.InMemory}, {"https", applebackemu.HTTPS}} {
		t.Run(mode.name, func(t *testing.T) {
			start, _ := url.Parse("https://download.itunes.apple.com/start")
			cdn, _ := url.Parse("https://cdn.mzstatic.com/archive")
			steps := []applebackemu.Step{
				applebackemu.DownloadAsset(start.String(), applebackemu.Response{StatusCode: 302, Header: http.Header{"Location": {"https://download.itunes.apple.com/continue"}, "Set-Cookie": {"asset-ticket=issued; Path=/; Secure"}}}),
				applebackemu.DownloadAsset("https://download.itunes.apple.com/continue", applebackemu.Response{StatusCode: 302, Header: http.Header{"Location": {cdn.String()}, "Set-Cookie": {"asset-ticket=rotated; Path=/; Secure"}}}),
				applebackemu.DownloadAsset(cdn.String(), applebackemu.Response{Header: http.Header{"Set-Cookie": {"cdn-ticket=rotated; Path=/; Secure"}}, Body: []byte("synthetic archive")}),
			}
			steps[0].Header.Set("Cookie", "store-auth=native")
			steps[1].Header.Set("Cookie", "store-auth=native; asset-ticket=issued")
			steps[2].Header.Set("Cookie", "cdn-ticket=previous")
			session, backend := downloadBackend(t, mode.mode, downloadAccount(), start.String(), steps...)
			jar := session.Client.Jar
			jar.SetCookies(start, []*http.Cookie{{Name: "store-auth", Value: "native", Domain: ".apple.com", Path: "/", Secure: true}})
			jar.SetCookies(cdn, []*http.Cookie{{Name: "cdn-ticket", Value: "previous", Path: "/", Secure: true}})
			if err := session.fetchToFile(context.Background(), start.String(), filepath.Join(t.TempDir(), "archive"), "", nil); err != nil {
				t.Fatal(err)
			}
			if len(backend.Requests()) != 3 {
				t.Fatal("redirect sequence incomplete")
			}
			raw, err := session.Export()
			if err != nil {
				t.Fatal(err)
			}
			restored, err := Restore(raw)
			if err != nil {
				t.Fatal(err)
			}
			for endpoint, name := range map[*url.URL]string{start: "asset-ticket", cdn: "cdn-ticket"} {
				found := false
				for _, cookie := range restored.Client.Jar.Cookies(endpoint) {
					found = found || (cookie.Name == name && cookie.Value == "rotated")
				}
				if !found {
					t.Fatalf("download cookie missing after Export/Restore: %s", name)
				}
			}

			if mode.mode == applebackemu.HTTPS {
				requests := backend.Requests()
				if requests[0].ServerName != "download.itunes.apple.com" || requests[2].ServerName != "cdn.mzstatic.com" || requests[2].Protocol != "HTTP/2.0" {
					t.Fatal("asset TLS routing lost")
				}
			}
		})
	}
}
