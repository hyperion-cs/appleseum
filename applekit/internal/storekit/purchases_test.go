package storekit

import (
	"context"
	"strings"
	"testing"

	"github.com/hyperion-cs/appleseum/applekit/internal/applebackemu"
)

func TestCatalogKeepsPurchaseArtworkWithinAccountCountry(t *testing.T) {
	for _, mode := range []struct {
		name string
		mode applebackemu.Mode
	}{{"memory", applebackemu.InMemory}, {"https", applebackemu.HTTPS}} {
		t.Run(mode.name, func(t *testing.T) {
			const history = `{"purchases":[{"orderId":"fixture-order","invoiceDate":"2026-01-10T12:00:00Z","lineItems":[{"adamId":"1","kind":"IOSApp","nameForDisplay":"Delisted fixture","purchaseDate":"2026-01-09T12:00:00Z","artworkUrl":"https://is1-ssl.mzstatic.com/history-one/{w}x{h}{c}-{q}.{f}"},{"adamId":"2","kind":"IOSApp","nameForDisplay":"Listed fixture","purchaseDate":null,"artworkUrl":"https://is1-ssl.mzstatic.com/history-two/{w}x{h}bb.{f}"},{"adamId":"3","kind":"IOSApp","nameForDisplay":"No artwork fixture","purchaseDate":null,"artworkUrl":null},{"adamId":"4","kind":"IOSApp","nameForDisplay":"Invalid artwork fixture","purchaseDate":null,"artworkUrl":42}]}]}`
			session, backend := catalogBackend(t, mode.mode,
				settingsStep(t, 2_026), historyStep(t, 2_026, "", history),
				lookupStep(t, "kz", []int64{1, 2, 3, 4}, `{"results":[{"trackId":2,"artworkUrl100":"https://is1-ssl.mzstatic.com/listed.png"}]}`),
			)
			lastProgress := -1.0
			intermediate := false
			apps, err := session.Catalog(context.Background(), func(value float64) {
				if value < lastProgress || value < 0 || value > 100 {
					t.Errorf("invalid progress %v after %v", value, lastProgress)
				}
				if value > 0 && value < 100 {
					intermediate = true
				}
				lastProgress = value
			})
			if !intermediate || lastProgress != 100 {
				t.Fatal("catalog did not report work progress")
			}

			if err != nil {
				t.Fatal(err)
			}
			if len(backend.Requests()) != 3 || len(apps) != 4 {
				t.Fatal("catalog did not use the existing lookup batch")
			}
			if !apps[0].Delisted || apps[0].ArtworkUrl != "https://is1-ssl.mzstatic.com/history-one/120x120bb.jpg" {
				t.Fatal("delisted artwork was not retained from purchase history")
			}
			if apps[0].PurchasedAt != "2026-01-09" || apps[1].PurchasedAt != "2026-01-10" {
				t.Fatal("catalog lost item purchase date or invoice fallback")
			}
			if apps[1].Delisted || apps[1].ArtworkUrl != "https://is1-ssl.mzstatic.com/listed.png" {
				t.Fatal("listed artwork did not prefer current store metadata")
			}
			if apps[2].ArtworkUrl != "" || apps[3].ArtworkUrl != "" {
				t.Fatal("invalid optional artwork affected catalog")
			}

			if mode.mode == applebackemu.HTTPS {
				requests := backend.Requests()
				if requests[0].ServerName != "ph2.itunes.apple.com" || requests[0].Protocol != "HTTP/2.0" || requests[2].ServerName != "itunes.apple.com" {
					t.Fatal("catalog TLS routing or profile lost")
				}
			}
		})
	}
}

func TestPurchaseArtworkTemplates(t *testing.T) {
	for _, test := range []struct{ input, want string }{
		{"https://is1-ssl.mzstatic.com/a/{w}x{h}{c}.{f}", "https://is1-ssl.mzstatic.com/a/120x120bb.jpg"},
		{"https://is1-ssl.mzstatic.com/a/{w}x{h}{c}-{q}.{f}", "https://is1-ssl.mzstatic.com/a/120x120bb.jpg"},
		{"https://is1-ssl.mzstatic.com/a/{w}x{h}{c}{q}.{f}", "https://is1-ssl.mzstatic.com/a/120x120bb.jpg"},
		{"https://is1-ssl.mzstatic.com/static.png", "https://is1-ssl.mzstatic.com/static.png"},
		{"", ""},
		{"https://is1-ssl.mzstatic.com/{unknown}.png", ""},
		{strings.Repeat("a", 4_097), ""},
	} {
		if got := purchaseArtworkUrl(test.input); got != test.want {
			t.Fatalf("unexpected artwork template result: %q", got)
		}
	}
}

// Storefront lookup results must not remove apps from purchase history.
func TestCatalogRetainsPurchasedVkAndMusicWhenLookupOmitsThem(t *testing.T) {
	const history = `{"purchases":[{"orderId":"synthetic-vk-order","invoiceDate":null,"lineItems":[{"adamId":"564177498","kind":"IOSApp","nameForDisplay":"VK: social network, messenger","purchaseDate":null,"artworkUrl":"https://is1-ssl.mzstatic.com/vk/{w}x{h}{c}.{f}"},{"adamId":"1054372220","kind":"IOSApp","nameForDisplay":"VK Музыка: песни и аудиокниги","purchaseDate":null,"artworkUrl":null},{"adamId":"1168929380","kind":"BaseSubscription","nameForDisplay":"VK music subscription","purchaseDate":null,"artworkUrl":null}]}]}`
	session, backend := catalogBackend(t, applebackemu.InMemory, settingsStep(t, 2_026), historyStep(t, 2_026, "", history), lookupStep(t, "kz", []int64{564_177_498, 1_054_372_220}, `{"results":[]}`))
	apps, err := session.Catalog(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(apps) != 2 || len(backend.Requests()) != 3 {
		t.Fatal("lost owned app, included subscription, or queried lookup per app")
	}
	for i, id := range []int64{564_177_498, 1_054_372_220} {
		if apps[i].Id != id || !apps[i].Delisted || apps[i].Name == "" {
			t.Fatal("delisted app disappeared from purchased catalog")
		}
	}
	if apps[0].ArtworkUrl != "https://is1-ssl.mzstatic.com/vk/120x120bb.jpg" {
		t.Fatal("delisted VK lost purchase artwork")
	}
}
