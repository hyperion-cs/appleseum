package configurator

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/hyperion-cs/appleseum/applekit/internal/grandslam"
)

func TestFlowSynchronizesOneExplicitDeviceChallenge(t *testing.T) {
	for _, mode := range []string{"accepted", "repeated-challenge", "password-rejected", "sync-rejected"} {
		t.Run(mode, func(t *testing.T) {
			flow := testFlow(t)
			calls, syncs := 0, 0
			challenge := &grandslam.StatusError{HttpStatus: 200, Status: grandslam.Status{Code: -22_421, ServerStatus: 434, DeviceTarget: "native", DeviceData: "synthetic-sim"}}

			flow.synchronizeDevice = func(context.Context, *grandslam.StatusError) error {
				syncs++
				if mode == "sync-rejected" {
					return errors.New("synthetic sync rejection")
				}
				return nil
			}

			flow.refreshDevice = func() (DeviceSnapshot, error) {
				device := testDevice()
				if syncs > 0 {
					device.Native.Md = "fresh-md"
					device.Cpd["X-Apple-I-MD"] = "fresh-md"
				}
				return device, nil
			}

			flow.deps.authenticate = func(_ context.Context, client *http.Client, _ grandslam.Metadata, appleId, password string, cpd map[string]any) (grandslam.SessionData, error) {
				calls++
				if client != flow.http || appleId != "person@example.invalid" || password != "synthetic-password" {
					t.Fatal("retry changed identity or client")
				}
				if mode == "password-rejected" {
					return grandslam.SessionData{}, &grandslam.StatusError{Status: grandslam.Status{Code: -22_406}}
				}
				if calls == 1 || mode == "repeated-challenge" {
					return grandslam.SessionData{}, challenge
				}
				if cpd["X-Apple-I-MD"] != "fresh-md" {
					t.Fatal("retry retained stale device headers")
				}
				return grandslam.SessionData{DirectoryServicesId: "123", Tokens: map[string]grandslam.ServiceToken{"com.apple.gs.idms.pet": {Token: "synthetic-pet"}}}, nil
			}

			result, err := flow.AuthenticatePassword(context.Background(), "person@example.invalid", "synthetic-password")
			if mode == "accepted" {
				if err != nil || result.Identity == nil || calls != 2 || syncs != 1 {
					t.Fatal("device sync did not continue SRP", err)
				}
			} else if err == nil {
				t.Fatal("rejected flow succeeded")
			}
			if mode == "repeated-challenge" && (calls != 2 || syncs != 1) {
				t.Fatal("unbounded sync retry")
			}
			if mode == "password-rejected" && (calls != 1 || syncs != 0) {
				t.Fatal("password error triggered device sync")
			}
			if mode == "sync-rejected" && (calls != 1 || syncs != 1) {
				t.Fatal("failed sync retried credentials")
			}
		})
	}
}
