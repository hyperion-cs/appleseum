package ams

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"strconv"

	"github.com/hyperion-cs/appleseum/applekit/internal/applebackemu"
)

type nativeSigner struct {
	body []byte
	err  error
}

func (s *nativeSigner) Sign(body []byte) ([]byte, error) {
	s.body = bytes.Clone(body)
	if s.err != nil {
		return nil, s.err
	}
	return bytes.Repeat([]byte{0x5a}, nativeSignatureLength), nil
}

func nativeFixture(t *testing.T, mode applebackemu.Mode, steps ...applebackemu.Step) *applebackemu.Server {
	t.Helper()
	fixture, err := applebackemu.New(mode, steps...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		fixture.Close()
		if err := fixture.Verify(); err != nil {
			t.Error(err)
		}
	})
	return fixture
}

func nativeStep(t *testing.T, c NativeCredentials, d NativeDevice, o NativeOptions) applebackemu.Step {
	t.Helper()
	header := http.Header{}
	for name, value := range map[string]string{
		"Accept-Language": o.Context.Language, "X-Apple-Store-Front": o.Context.Storefront, "X-Apple-Tz": strconv.Itoa(o.Context.UtcOffsetSeconds),
		"X-Apple-I-MD": d.Md, "X-Apple-I-MD-M": d.MachineId, "X-Apple-I-MD-LU": d.LocalUser, "X-Apple-I-MD-RINFO": d.Routing,
		"X-Apple-I-Client-Time": o.ClientTime.UTC().Format(time.RFC3339), "X-Apple-I-Locale": o.Locale, "X-Apple-I-TimeZone": o.TimeZone, "X-Apple-Age-Area-Compliance-ID": o.ComplianceId,
	} {
		header.Set(name, value)
	}
	if d.Amd != "" {
		header.Set("X-Apple-AMD", d.Amd)
		header.Set("X-Apple-AMD-M", d.AmdMachineId)
	}
	step, err := applebackemu.NativeAuthentication(applebackemu.NativeOptions{
		Account: applebackemu.AuthAccount{AppleId: c.AppleId, Pet: c.PasswordEquivalent, Adsid: c.Adsid, DirectoryServicesId: c.DirectoryServicesId}, Guid: c.Guid, Header: header,
		Signature: bytes.Repeat([]byte{0x5a}, 501), PasswordToken: "synthetic-password-token", AlternateDsid: "synthetic-alt-dsid", Storefront: "143517-2,34", Pod: "18",
	})
	if err != nil {
		t.Fatal(err)
	}
	return step
}

func validNativeInput() (NativeCredentials, NativeDevice, NativeOptions) {
	credentials := NativeCredentials{
		AppleId: "owner@example.test", PasswordEquivalent: "synthetic-pet=PET",
		Adsid: "synthetic-adsid", DirectoryServicesId: "123456789", Guid: "001122334455",
	}
	device := NativeDevice{
		LocalUser: "synthetic-local-user", Md: "synthetic-md",
		MachineId: "synthetic-md-m", Routing: "84215040", Amd: "synthetic-amd", AmdMachineId: "synthetic-amd-m",
	}
	options := NativeOptions{
		Context: RequestContext{Actor: Configurator, Storefront: "143517-2,34", Language: "en-RU", UtcOffsetSeconds: 10_800},
		Locale:  "en_RU", TimeZone: "GMT+3", ComplianceId: "RU", ClientTime: time.Date(2_026, 10, 29, 12, 34, 56, 0, time.FixedZone("test", 3*60*60)),
	}
	return credentials, device, options
}

func TestAuthenticateNativeRequestContract(t *testing.T) {
	for _, mode := range []struct {
		name string
		mode applebackemu.Mode
	}{{"memory", applebackemu.InMemory}, {"https", applebackemu.HTTPS}} {
		t.Run(mode.name, func(t *testing.T) {
			credentials, device, options := validNativeInput()
			signer := &nativeSigner{}
			step := nativeStep(t, credentials, device, options)
			step.Header.Set("Cookie", "synthetic=cookie")
			fixture := nativeFixture(t, mode.mode, step)
			client := fixture.Client()
			cookieUrl, _ := url.Parse("https://auth.itunes.apple.com/")
			client.Jar.SetCookies(cookieUrl, []*http.Cookie{{Name: "synthetic", Value: "cookie"}})
			session, err := AuthenticateNative(context.Background(), client, "https://auth.itunes.apple.com/auth/v1/native", signer, credentials, device, options)
			if err != nil {
				t.Fatal(err)
			}
			if session.PasswordToken != "synthetic-password-token" || session.DirectoryServicesId != "123456789" || session.AlternateDsid != "synthetic-alt-dsid" || session.Storefront != "143517-2,34" || session.Pod != "18" {
				t.Fatal("native session fields mismatch")
			}
			request := fixture.Requests()[0]
			if !bytes.Equal(request.Body, signer.body) {
				t.Fatal("signed body differs from sent body")
			}
			if mode.mode == applebackemu.HTTPS && (request.ServerName != "auth.itunes.apple.com" || request.Protocol != "HTTP/2.0") {
				t.Fatal("native TLS routing lost")
			}
		})
	}
}

func TestAuthenticateNativeStorefrontHeaders(t *testing.T) {
	credentials, device, options := validNativeInput()
	for _, tc := range []struct{ name, set, request, want string }{
		{"request header from live response", "", "143517-2,34", "143517-2,34"},
		{"explicit change takes precedence", "143441-1,29", "143517-2,34", "143441-1,29"},
		{"missing remains missing", "", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			step := nativeStep(t, credentials, device, options)
			step.Response.Header.Del("X-Set-Apple-Store-Front")
			step.Response.Header.Set("X-Set-Apple-Store-Front", tc.set)
			step.Response.Header.Set("X-Apple-Request-Store-Front", tc.request)
			client := nativeFixture(t, applebackemu.InMemory, step).Client()
			got, err := AuthenticateNative(context.Background(), client, "https://auth.itunes.apple.com/auth/v1/native", &nativeSigner{}, credentials, device, options)
			if err != nil {
				t.Fatal(err)
			}
			if got.Storefront != tc.want {
				t.Fatal("native storefront header selection changed")
			}
		})
	}
}

func TestAuthenticateNativeRejectsInvalidInputBeforeSigning(t *testing.T) {
	credentials, device, options := validNativeInput()
	for _, test := range []struct {
		name     string
		endpoint string
		mutate   func(*NativeCredentials, *NativeDevice, *NativeOptions)
	}{
		{"endpoint", "https://apple.com/auth/v1/native", func(*NativeCredentials, *NativeDevice, *NativeOptions) {}},
		{"legacy actor", "https://auth.itunes.apple.com/auth/v1/native", func(_ *NativeCredentials, _ *NativeDevice, o *NativeOptions) { o.Context.Actor = Accounts }},
		{"missing PET", "https://auth.itunes.apple.com/auth/v1/native", func(c *NativeCredentials, _ *NativeDevice, _ *NativeOptions) { c.PasswordEquivalent = "" }},
		{"header injection", "https://auth.itunes.apple.com/auth/v1/native", func(_ *NativeCredentials, d *NativeDevice, _ *NativeOptions) { d.Amd = "value\r\nInjected: yes" }},
		{"zero time", "https://auth.itunes.apple.com/auth/v1/native", func(_ *NativeCredentials, _ *NativeDevice, o *NativeOptions) { o.ClientTime = time.Time{} }},
	} {
		t.Run(test.name, func(t *testing.T) {
			c, d, o := credentials, device, options
			test.mutate(&c, &d, &o)
			signer := &nativeSigner{}
			client := nativeFixture(t, applebackemu.InMemory).Client()
			if _, err := AuthenticateNative(context.Background(), client, test.endpoint, signer, c, d, o); err == nil {
				t.Fatal("invalid native input accepted")
			}
			if len(signer.body) != 0 {
				t.Fatal("invalid native input reached signer")
			}
		})
	}
}

func TestAuthenticateNativeResponseFailures(t *testing.T) {
	credentials, device, options := validNativeInput()
	for _, test := range []struct {
		name   string
		status int
		body   string
		reason string
	}{
		{"upstream", 503, "private upstream text", "upstream_http"},
		{"empty", 200, "", "invalid_response"},
		{"unrecognized_result", 200, `<?xml version="1.0"?><plist version="1.0"><dict><key>status</key><integer>1</integer></dict></plist>`, "unrecognized_result"},
		{"missing session", 200, `<?xml version="1.0"?><plist version="1.0"><dict><key>status</key><integer>0</integer></dict></plist>`, "missing_session"},
		{"identity mismatch", 200, `<?xml version="1.0"?><plist version="1.0"><dict><key>status</key><integer>0</integer><key>passwordToken</key><string>token</string><key>dsPersonId</key><string>987654321</string></dict></plist>`, "identity_mismatch"},
	} {
		t.Run(test.name, func(t *testing.T) {
			step := nativeStep(t, credentials, device, options)
			step.Response = applebackemu.Response{StatusCode: test.status, Body: []byte(test.body)}
			client := nativeFixture(t, applebackemu.InMemory, step).Client()
			_, err := AuthenticateNative(context.Background(), client, "https://auth.itunes.apple.com/auth/v1/native", &nativeSigner{}, credentials, device, options)
			var nativeErr *NativeError
			if !errors.As(err, &nativeErr) || nativeErr.Reason != test.reason || strings.Contains(err.Error(), "private upstream text") {
				t.Fatalf("unexpected native error: %v", err)
			}
		})
	}

	signerErr := errors.New("synthetic signer failure")
	_, err := AuthenticateNative(context.Background(), nativeFixture(t, applebackemu.InMemory).Client(), "https://auth.itunes.apple.com/auth/v1/native", &nativeSigner{err: signerErr}, credentials, device, options)
	if !errors.Is(err, signerErr) {
		t.Fatal("signer failure was not preserved")
	}
}

type changingNativeSigner struct{ calls int }

func (s *changingNativeSigner) Sign([]byte) ([]byte, error) {
	s.calls++
	return bytes.Repeat([]byte{byte(s.calls)}, nativeSignatureLength), nil
}

func TestNativeRetryUsesFreshCookiesAmdAndSignature(t *testing.T) {
	credentials, device, options := validNativeInput()
	device.Amd, device.AmdMachineId = "", ""
	options.Amd = &AmdController{Provider: &fakeAmdProvider{provisioned: true}, Bag: Bag{}}
	signer := &changingNativeSigner{}
	first := nativeStep(t, credentials, device, options)
	first.Header.Set("Cookie", "session=old")
	first.Header.Set("X-Apple-ActionSignature", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 501)))
	first.Header.Set("X-Apple-AMD", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x44}, 24)))
	first.Header.Set("X-Apple-AMD-M", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x55}, 60)))
	first.Response.Header = http.Header{"X-Apple-Amd-Action": {"RP"}, "Set-Cookie": {"session=rotated; Path=/; Secure"}}
	second := nativeStep(t, credentials, device, options)
	second.Header.Set("Cookie", "session=rotated")
	second.Header.Set("X-Apple-ActionSignature", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{2}, 501)))
	second.Header["X-Apple-Amd"], second.Header["X-Apple-Amd-M"] = nil, nil
	fixture := nativeFixture(t, applebackemu.InMemory, first, second)
	u, _ := url.Parse("https://auth.itunes.apple.com/")
	fixture.Client().Jar.SetCookies(u, []*http.Cookie{{Name: "session", Value: "old", Path: "/", Secure: true}})
	if _, err := AuthenticateNative(context.Background(), fixture.Client(), u.String()+"auth/v1/native", signer, credentials, device, options); err != nil || signer.calls != 2 {
		t.Fatal("retry failed", err)
	}
	requests := fixture.Requests()
	if len(requests) != 2 || !bytes.Equal(requests[0].Body, requests[1].Body) {
		t.Fatal("retry changed native body")
	}
}
