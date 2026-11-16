package artwork

import (
	"context"
	"net/http"
	"testing"

	"github.com/hyperion-cs/appleseum/applekit/internal/applebackemu"
)

func TestArtworkRetainsItsCookiesAcrossRedirectsAndFetches(t *testing.T) {
	for _, mode := range []struct {
		name string
		mode applebackemu.Mode
	}{{"memory", applebackemu.InMemory}, {"https", applebackemu.HTTPS}} {
		t.Run(mode.name, func(t *testing.T) {
			steps := []applebackemu.Step{
				applebackemu.Artwork("https://is1.mzstatic.com/start", applebackemu.Response{StatusCode: 302, Header: http.Header{"Location": {"https://is1.mzstatic.com/final"}, "Set-Cookie": {"image-ticket=issued; Path=/; Secure"}}}),
				applebackemu.Artwork("https://is1.mzstatic.com/final", applebackemu.Response{Body: []byte("synthetic image")}),
				applebackemu.Artwork("https://is1.mzstatic.com/start", applebackemu.Response{Body: []byte("synthetic image")}),
			}
			steps[0].Header["Cookie"] = nil
			steps[1].Header.Set("Cookie", "image-ticket=issued")
			steps[2].Header.Set("Cookie", "image-ticket=issued")
			backend := applebackemu.NewTest(t, mode.mode, steps...)
			client := NewClient(backend.Client().Transport)
			defer client.CloseIdleConnections()
			for range 2 {
				if _, err := client.Fetch(context.Background(), "https://is1.mzstatic.com/start"); err != nil {
					t.Fatal(err)
				}
			}
			if len(backend.Requests()) != 3 {
				t.Fatal("redirect and later request were not exercised")
			}

			if mode.mode == applebackemu.HTTPS && backend.Requests()[0].ServerName != "is1.mzstatic.com" {
				t.Fatal("artwork TLS routing lost")
			}
		})
	}
}
