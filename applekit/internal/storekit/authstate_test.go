package storekit

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/hyperion-cs/appleseum/applekit/internal/ams"
	"github.com/hyperion-cs/appleseum/applekit/internal/applebackemu"
	"github.com/hyperion-cs/appleseum/applekit/internal/httpx"
)

func TestAuthenticationRejectionClassification(t *testing.T) {
	now := time.Date(2_026, 10, 26, 9, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name    string
		status  int
		body    string
		expired bool
	}{
		{"explicit forbidden", 403, `{"errors":[{"code":"authentication"}]}`, true},
		{"forbidden HTML", 403, "<html>Request blocked</html>", false},
		{"permission denied", 403, `{"errors":[{"code":"permission"}]}`, false},
		{"unauthorized", 401, "", true},
		{"rate limit", 429, `{"errors":[{"code":"authentication"}]}`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			step := settingsStep(t)
			step.Response = catalogReply(test.body)
			step.Response.StatusCode = test.status
			s, _ := catalogBackend(t, applebackemu.InMemory, step)
			s.authenticatedAt = now.Add(-time.Hour)
			s.lastSuccessAt = now.Add(-20 * time.Minute)

			s.clock = func() time.Time { return now }

			_, err := s.ListPurchases(context.Background())
			if errors.Is(err, ErrSessionExpired) != test.expired {
				t.Fatalf("incorrect expiry classification for HTTP %d", test.status)
			}
			if test.expired {
				var rejected *SessionRejection
				if !errors.As(err, &rejected) || !rejected.AgeKnown || rejected.Age != time.Hour || rejected.Idle != 20*time.Minute {
					t.Fatal("session age diagnostic missing")
				}
			}
		})
	}
}

func TestSessionLifetimeMetadataRoundTrip(t *testing.T) {
	start := time.Date(2_026, 10, 26, 9, 0, 0, 0, time.UTC)
	s := &Session{Account: Account{DirectoryServicesId: "123", PasswordToken: "token", Guid: "001122334455"}, Client: httpx.NewClient(newJar()), amd: ams.AmdController{Bag: ams.Bag{DownloadURL: "https://buy.example.test/download"}}, authenticatedAt: start, lastSuccessAt: start.Add(time.Minute)}
	data, err := s.Export()
	if err != nil {
		t.Fatal(err)
	}
	restored, err := Restore(data)
	if err != nil {
		t.Fatal(err)
	}
	if !restored.authenticatedAt.Equal(start) || !restored.lastSuccessAt.Equal(start.Add(time.Minute)) {
		t.Fatal("session dates changed across restart")
	}
	var legacy map[string]json.RawMessage
	json.Unmarshal(data, &legacy)
	delete(legacy, "AuthenticatedAt")
	delete(legacy, "LastSuccessAt")
	data, _ = json.Marshal(legacy)
	restored, err = Restore(data)
	if err != nil || !restored.authenticatedAt.IsZero() {
		t.Fatal("legacy session lost compatibility")
	}
}

func TestDownloadReauthenticationReasons(t *testing.T) {
	for _, entry := range []struct{ code, reason string }{{"2034", "token_expired"}, {"2042", "sign_in_required"}, {"1008", "device_verification"}} {
		t.Run(entry.reason, func(t *testing.T) {
			account := downloadAccount()
			response := downloadReply(t, map[string]any{"failureType": entry.code})
			step := dispatchStep(t, account, fixtureDispatchUrl, 1, "fixture-key", "1", response)
			s, _ := downloadBackend(t, applebackemu.InMemory, account, fixtureDispatchUrl, step)
			_, err := s.resolveDownload(context.Background(), 1, "fixture-key", "1", false)
			var rejected *SessionRejection
			if !errors.As(err, &rejected) || rejected.Reason != entry.reason || !errors.Is(err, ErrSessionExpired) {
				t.Fatal("download rejection reason was lost")
			}
		})
	}
}
