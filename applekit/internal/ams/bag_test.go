package ams

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/hyperion-cs/appleseum/applekit/internal/applebackemu"
)

const (
	bagFixture = applebackemu.BagJson
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func mustBagUrl(t *testing.T) *url.URL {
	t.Helper()
	u, err := url.Parse("https://bag.itunes.apple.com/bag.xml")
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestFetchActorBag(t *testing.T) {
	for _, mode := range []struct {
		name string
		mode applebackemu.Mode
	}{{"memory", applebackemu.InMemory}, {"https", applebackemu.HTTPS}} {
		for _, actor := range []struct {
			name        string
			clientActor Actor
			serverActor applebackemu.BagActor
		}{{"Configurator", Configurator, applebackemu.Configurator}, {"Accounts", Accounts, applebackemu.Accounts}} {
			t.Run(mode.name+"/"+actor.name, func(t *testing.T) {
				metadata := RequestContext{Actor: actor.clientActor, Storefront: "143517-2,34", Language: "en-RU", UtcOffsetSeconds: 10_800}
				options := applebackemu.BagOptions{Actor: actor.serverActor, Storefront: "143517-2,34", Language: "en-RU", UtcOffsetSeconds: 10_800}
				first, err := applebackemu.ServiceBag(options)
				if err != nil {
					t.Fatal(err)
				}
				second, err := applebackemu.ServiceBag(options)
				if err != nil {
					t.Fatal(err)
				}
				first.Header["Cookie"] = nil
				second.Header["Cookie"] = []string{"bag-test=synthetic"}
				fixture, err := applebackemu.New(mode.mode, first, second)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					fixture.Close()
					if err := fixture.Verify(); err != nil {
						t.Error(err)
					}
				})
				for range 2 {
					bag, err := FetchBag(context.Background(), fixture.Client(), metadata)
					if err != nil {
						t.Fatal(err)
					}
					if !strings.Contains(bag.MDFinishTemplate, "{p$itspod$-}") || len(bag.SAPRequestRules["auth/v1"]) != 1 || bag.AuthenticateURL == "" {
						t.Fatal("bag fields lost")
					}
				}
				cookies := fixture.Client().Jar.Cookies(mustBagUrl(t))
				if len(cookies) != 1 || cookies[0].Value != "synthetic" {
					t.Fatal("injected cookie jar was lost")
				}
				for _, request := range fixture.Requests() {
					if mode.mode == applebackemu.HTTPS && (request.ServerName != "bag.itunes.apple.com" || request.TlsVersion == 0 || request.Protocol != "HTTP/2.0") {
						t.Fatalf("missing Apple SNI, TLS or HTTP/2: %+v", request)
					}
					if mode.mode == applebackemu.InMemory && request.TlsVersion != 0 {
						t.Fatal("memory transport unexpectedly used TLS")
					}
				}
				h1, _ := metadata.Headers()
				h1.Set("Accept", "changed")
				h2, _ := metadata.Headers()
				if h2.Get("Accept") != "*/*" {
					t.Fatal("headers reused across requests")
				}
			})
		}
	}
}

func TestInvalidRequestMetadata(t *testing.T) {
	valid := RequestContext{Storefront: "143517-2,34", Language: "en-RU"}
	for _, mutate := range []func(*RequestContext){
		func(c *RequestContext) { c.Actor = 99 }, func(c *RequestContext) { c.Storefront = "------" }, func(c *RequestContext) { c.Storefront = "143517--2,34" }, func(c *RequestContext) { c.Storefront = "143517\r\nX-Test: bad" },
		func(c *RequestContext) { c.Language = "" }, func(c *RequestContext) { c.Language = "ru\nheader" }, func(c *RequestContext) { c.Language = strings.Repeat("a", 101) }, func(c *RequestContext) { c.UtcOffsetSeconds = 86_401 },
	} {
		metadata := valid
		mutate(&metadata)
		client := &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
			t.Fatal("invalid metadata reached transport")
			return nil, nil
		})}
		if _, err := FetchBag(context.Background(), client, metadata); err == nil {
			t.Fatal("invalid metadata accepted")
		}
	}
}

func TestBagContracts(t *testing.T) {
	for _, value := range []string{"null", "{}", `{"urlBag":{}}`, "<plist/>", strings.Repeat(" ", 2_097_152+1)} {
		if _, err := ParseBag([]byte(value)); err == nil {
			t.Fatal("invalid bag accepted")
		}
	}
	for _, endpoint := range []string{"http://auth.itunes.apple.com/", "https://apple.com.attacker.invalid/", "https://user@auth.itunes.apple.com/", "https://auth.itunes.apple.com/#fragment", "https://%"} {
		var fixture map[string]any
		if err := json.Unmarshal([]byte(bagFixture), &fixture); err != nil {
			t.Fatal(err)
		}
		fixture["authenticateAccount"] = endpoint
		body, err := json.Marshal(fixture)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ParseBag(body); err == nil {
			t.Fatal("invalid endpoint accepted")
		}
	}
	bag, err := ParseBag([]byte(bagFixture))
	if err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{"auth.itunes.apple.com", "p18-buy.itunes.apple.com", "api.g.apple.com", "AUTH.ITUNES.APPLE.COM."} {
		if !bag.NeedsAmd(host) {
			t.Errorf("AMD rule missed %s", host)
		}
	}
	for _, host := range []string{"auth.itunes.apple.com.attacker.invalid", "other.itunes.apple.com", "g.apple.com", ""} {
		if bag.NeedsAmd(host) {
			t.Errorf("AMD rule escaped domain %s", host)
		}
	}
	bag.AMDDomains = []string{"*"}
	if bag.NeedsAmd("example.com") {
		t.Fatal("empty wildcard suffix accepted")
	}
}

func TestUncachedNotModifiedBag(t *testing.T) {
	step, err := applebackemu.ServiceBag(applebackemu.BagOptions{Storefront: "143517", Language: "ru"})
	if err != nil {
		t.Fatal(err)
	}
	step.Response.StatusCode = 304
	step.Response.Body = nil
	client := nativeFixture(t, applebackemu.InMemory, step).Client()
	if _, err := FetchBag(context.Background(), client, RequestContext{Storefront: "143517", Language: "ru"}); err == nil {
		t.Fatal("304 without cached body accepted")
	}
}
