package grandslam

import (
	"context"
	"errors"
	"strings"
	"testing"

	"howett.net/plist"

	"github.com/hyperion-cs/appleseum/applekit/internal/applebackemu"
)

func verificationBody(t *testing.T, fields map[string]any) []byte {
	t.Helper()
	b, err := plist.Marshal(fields, plist.XMLFormat)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestTrustedDeviceExchange(t *testing.T) {
	for _, mode := range []struct {
		name string
		mode applebackemu.Mode
	}{{"memory", applebackemu.InMemory}, {"https", applebackemu.HTTPS}} {
		t.Run(mode.name, func(t *testing.T) {
			session := SessionData{Adsid: "synthetic-adsid", Idms: "synthetic-idms"}
			cpd := testCpd()
			cpd["X-Apple-Locale"], cpd["X-Apple-I-TimeZone"] = "en_GB", "UTC"
			steps, err := applebackemu.TrustedDevice(applebackemu.TrustedDeviceOptions{Device: authDevice(cpd), Adsid: session.Adsid, Idms: session.Idms, Code: "123456"})
			if err != nil {
				t.Fatal(err)
			}
			s, fixture := serviceFixture(t, mode.mode, steps...)
			if err := s.RequestTrustedDeviceCode(context.Background(), session, cpd); err != nil {
				t.Fatal(err)
			}
			if err := s.VerifyTrustedDeviceCode(context.Background(), session, cpd, "123456"); err != nil {
				t.Fatal(err)
			}
			if len(fixture.Requests()) != 2 || cpd["Security-Code"] != nil || cpd["X-Apple-Identity-Token"] != nil {
				t.Fatal("unexpected requests or mutated caller snapshot")
			}
		})
	}
}

func TestTrustedDeviceRejectsInvalidConfirmation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   []byte
		code   int64
	}{
		{"wrong-code", 200, verificationBody(t, map[string]any{"ec": -21_669}), -21_669},
		{"wrong-code-with-http-error", 403, verificationBody(t, map[string]any{"ec": -21_669}), -21_669},
		{"missing-status", 200, verificationBody(t, map[string]any{"other": "value"}), 0},
		{"boolean-status", 200, verificationBody(t, map[string]any{"ec": false}), 0},
		{"string-status", 200, verificationBody(t, map[string]any{"ec": "0"}), 0},
		{"html", 200, []byte("<html>synthetic error</html>"), 0},
		{"empty", 204, nil, 0},
		{"http-error", 403, verificationBody(t, map[string]any{"ec": 0}), 0},
		{"redirect", 302, nil, 0},
		{"oversized", 200, []byte(strings.Repeat("x", maxServiceBody+1)), 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			step, err := applebackemu.TrustedDeviceVerification(applebackemu.TrustedDeviceOptions{Device: authDevice(testCpd()), Adsid: "synthetic", Idms: "token", Code: "123456"})
			if err != nil {
				t.Fatal(err)
			}
			step.Response = applebackemu.Response{StatusCode: tc.status, Body: tc.body, Header: map[string][]string{"Location": {"https://unexpected.invalid/verify"}}}
			s := testService(t, step)
			err = s.VerifyTrustedDeviceCode(context.Background(), SessionData{Adsid: "synthetic", Idms: "token"}, testCpd(), "123456")
			if err == nil {
				t.Fatal("invalid response accepted or unexpected retry/redirect")
			}
			if tc.code != 0 {
				var status *StatusError
				if !errors.As(err, &status) || status.Code != tc.code {
					t.Fatal("Apple verification status lost")
				}
			}
		})
	}
}

func TestTrustedDeviceRejectsInvalidInputWithoutNetwork(t *testing.T) {
	s := testService(t)
	session := SessionData{Adsid: "synthetic", Idms: "token"}
	for _, code := range []string{"", "12345", "1234567", "12 456", "12\n456", "abcdef"} {
		if err := s.VerifyTrustedDeviceCode(context.Background(), session, testCpd(), code); err == nil {
			t.Fatal("invalid verification code accepted")
		}
	}
	for _, session := range []SessionData{{}, {Adsid: "synthetic"}, {Adsid: "bad:id", Idms: "token"}} {
		if err := s.RequestTrustedDeviceCode(context.Background(), session, testCpd()); err == nil {
			t.Fatal("incomplete identity session accepted")
		}
	}
	cpd := testCpd()
	delete(cpd, "X-Apple-I-Client-Time")
	if err := s.RequestTrustedDeviceCode(context.Background(), session, cpd); err == nil {
		t.Fatal("missing snapshot time accepted")
	}
	cpd = testCpd()
	cpd["X-Apple-I-TimeZone"] = "UTC\r\ninjected: value"
	if err := s.RequestTrustedDeviceCode(context.Background(), session, cpd); err == nil {
		t.Fatal("invalid timezone accepted")
	}
}
