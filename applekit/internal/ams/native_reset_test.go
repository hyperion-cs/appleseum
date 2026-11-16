package ams

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"testing"

	"github.com/hyperion-cs/appleseum/applekit/internal/applebackemu"
)

func TestNativeStorefrontResetSession(t *testing.T) {
	credentials, device, options := validNativeInput()
	options.Context.Storefront = "143441"
	for _, tc := range []struct {
		name   string
		mutate func(map[string]any, http.Header)
		ok     bool
	}{
		{"complete server-directed country change", func(map[string]any, http.Header) {}, true},
		{"missing action", func(b map[string]any, _ http.Header) { delete(b, "action") }, false},
		{"other action", func(b map[string]any, _ http.Header) { b["action"] = map[string]any{"kind": "Other"} }, false},
		{"malformed action kind", func(b map[string]any, _ http.Header) {
			b["action"] = map[string]any{"kind": []string{"SetUserAndReset"}}
		}, false},
		{"not allowed", func(b map[string]any, _ http.Header) { b["m-allowed"] = false }, false},
		{"missing allowed", func(b map[string]any, _ http.Header) { delete(b, "m-allowed") }, false},
		{"missing token", func(b map[string]any, _ http.Header) { delete(b, "passwordToken") }, false},
		{"missing identity", func(b map[string]any, _ http.Header) { delete(b, "dsPersonId") }, false},
		{"other account", func(b map[string]any, _ http.Header) { b["dsPersonId"] = "987654321" }, false},
		{"other failure", func(b map[string]any, _ http.Header) { b["status"] = int64(-50) }, false},
		{"missing set storefront", func(_ map[string]any, h http.Header) { h.Del("X-Set-Apple-Store-Front") }, false},
		{"request storefront is not a directive", func(_ map[string]any, h http.Header) {
			h.Del("X-Set-Apple-Store-Front")
			h.Set("X-Apple-Request-Store-Front", "143517-2,34")
		}, false},
		{"invalid storefront", func(_ map[string]any, h http.Header) { h.Set("X-Set-Apple-Store-Front", "invalid") }, false},
		{"same country", func(_ map[string]any, h http.Header) { h.Set("X-Set-Apple-Store-Front", "143441-1,29") }, false},
		{"normal success ignores unrelated fields", func(b map[string]any, _ http.Header) {
			b["status"], b["action"], b["m-allowed"] = int64(0), "unrecognized optional data", "unrecognized optional data"
		}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := map[string]any{
				"status": int64(-128), "action": map[string]any{"kind": "SetUserAndReset"},
				"m-allowed": true, "passwordToken": "synthetic-issued-token", "dsPersonId": credentials.DirectoryServicesId,
			}
			header := make(http.Header)
			header.Set("X-Set-Apple-Store-Front", "143517-2,34")
			header.Add("Set-Cookie", "native-session=synthetic; Domain=.itunes.apple.com; Path=/; Secure")
			tc.mutate(body, header)
			response, err := applebackemu.PlistResponse(body)
			if err != nil {
				t.Fatal(err)
			}
			response.Header = header
			step := nativeStep(t, credentials, device, options)
			step.Response = response
			fixture := nativeFixture(t, applebackemu.InMemory, step)
			client := fixture.Client()
			got, err := AuthenticateNative(context.Background(), client, "https://auth.itunes.apple.com/auth/v1/native", &nativeSigner{}, credentials, device, options)
			if len(fixture.Requests()) != 1 {
				t.Fatal("native reset issued an extra authentication request")
			}
			if !tc.ok {
				var failure *NativeError
				if !errors.As(err, &failure) || failure.Reason != "unrecognized_result" || failure.ResultCode == nil || *failure.ResultCode != body["status"].(int64) {
					t.Fatal("non-successful reset was accepted or lost server code", err)
				}
				return
			}
			if err != nil || got.DirectoryServicesId != credentials.DirectoryServicesId || got.PasswordToken != "synthetic-issued-token" || got.Storefront != "143517-2,34" {
				t.Fatal("issued session was discarded", err)
			}
			u, _ := url.Parse("https://buy.itunes.apple.com/")
			cookies := client.Jar.Cookies(u)
			if len(cookies) != 1 || cookies[0].Name != "native-session" {
				t.Fatal("server-issued cookies were discarded")
			}
		})
	}
}
