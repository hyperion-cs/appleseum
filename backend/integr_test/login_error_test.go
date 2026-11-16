package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hyperion-cs/appleseum/applekit"

	"appleseum/backend/internal/account"
	server "appleseum/backend/internal/app"
	"appleseum/backend/internal/wire"
)

func TestLoginHttpSeparatesCredentialsCodeDeviceAndServiceFailures(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"password", &applekit.LoginError{Stage: "grandslam", Reason: "password_rejected", Status: 200, Failure: "-22406"}, 401, "password_rejected"},
		{"password with broader error", errors.Join(&applekit.LoginError{Stage: "grandslam", Reason: "password_rejected", Status: 200, Failure: "-22406"}, applekit.ErrCredentialsRejected), 401, "password_rejected"},
		{"password sentinel", applekit.ErrPasswordRejected, 401, "password_rejected"},
		{"unspecified sign-in details", applekit.ErrCredentialsRejected, 401, "account_credentials_rejected"},
		{"account details", errors.Join(&applekit.LoginError{Stage: "grandslam", Reason: "account_credentials_rejected", Status: 200, Failure: "-20970"}, applekit.ErrCredentialsRejected), 401, "account_credentials_rejected"},
		{"account details -20101", errors.Join(&applekit.LoginError{Stage: "grandslam", Reason: "account_credentials_rejected", Status: 200, Failure: "-20101"}, applekit.ErrCredentialsRejected), 401, "account_credentials_rejected"},
		{"verification", &applekit.LoginError{Stage: "verification", Reason: "verification_code_rejected", Status: 200, Failure: "-21669"}, 401, "verification_code_rejected"},
		{"device", &applekit.LoginError{Stage: "grandslam", Reason: "device_verification_required", Status: 200, Failure: "-22421"}, 502, "apple_device_verification_failed"},
		{"unknown", &applekit.LoginError{Stage: "grandslam", Reason: "unrecognized_result", Status: 200, Failure: "-99999"}, 502, "apple_response_unexpected"},
		{"network", &applekit.LoginError{Stage: "grandslam", Reason: "dns"}, 502, "apple_unavailable"},
		{"native", &applekit.LoginError{Stage: "native", Reason: "invalid_response", Status: 200}, 502, "apple_response_unexpected"},
		{"native unknown result", &applekit.LoginError{Stage: "native", Reason: "unrecognized_result", Status: 200, Failure: "-128"}, 502, "apple_response_unexpected"},
		{"missing session", &applekit.LoginError{Stage: "native", Reason: "missing_session", Status: 200}, 502, "apple_response_unexpected"},
		{"identity mismatch", &applekit.LoginError{Stage: "native", Reason: "identity_mismatch", Status: 200}, 502, "apple_response_unexpected"},
		{"action limit", &applekit.LoginError{Stage: "native", Reason: "amd_action_limit", Status: 200}, 502, "apple_response_unexpected"},
		{"processing", &applekit.LoginError{Stage: "sap", Reason: "protocol"}, 500, "login_processing_failed"},
		{"storage", &applekit.LoginError{Stage: "device_storage", Reason: "protocol"}, 500, "storage_failed"},
		{"unclassified", errors.New("private-error-marker"), 500, "login_processing_failed"},
		{"unknown reason", &applekit.LoginError{Stage: "native", Reason: "future-reason"}, 500, "login_processing_failed"},
		{"http forbidden", &applekit.LoginError{Stage: "native", Reason: "upstream_http", Status: 403}, 502, "apple_sign_in_denied"},
		{"http unavailable", &applekit.LoginError{Stage: "native", Reason: "upstream_http", Status: 503}, 502, "apple_unavailable"},
		{"expired", applekit.ErrVerificationExpired, 401, "verification_expired"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.MkdirAll(filepath.Join(dir, "packages"), 0700); err != nil {
				t.Fatal(err)
			}
			app, err := newTestServer(server.Config{DataDir: dir, Debug: true, DebugLogDir: filepath.Join(dir, "logs"), Apple: &testApple{signIn: func(context.Context, string, string, string) (account.Session, error) { return nil, tc.err }}})
			if err != nil {
				t.Fatal(err)
			}
			defer app.Close()
			httpServer := httptest.NewServer(app.Handler())
			defer httpServer.Close()
			jar, _ := cookiejar.New(nil)
			h := &harness{app: app, http: httpServer, client: &http.Client{Jar: jar}}
			var state wire.State
			h.request(t, "GET", "/api/state", nil, 200, &state)
			h.csrf = state.Csrf
			body := h.request(t, "POST", "/api/login", wire.LoginRequest{Email: "person@example.invalid", Password: "password-marker"}, tc.status, nil)
			var response struct {
				Code string `json:"code"`
			}
			if err = json.Unmarshal(body, &response); err != nil || response.Code != tc.code {
				t.Fatal("wrong authentication result", response, err)
			}
			var snapshot wire.DebugPage
			h.request(t, "GET", "/api/debug", nil, 200, &snapshot)
			seen := map[string]bool{}
			for _, event := range snapshot.Items {
				if event.Target == "/api/login" && event.Source == "API" {
					if event.Status != tc.status {
						t.Fatal("journal disagrees with HTTP response", event)
					}
					seen[event.Source] = true
				}
			}
			if seen["Auth"] || !seen["API"] {
				t.Fatal("login diagnostics missing")
			}
			encoded, _ := json.Marshal(snapshot)
			if strings.Contains(string(body)+string(encoded), "private-error-marker") {
				t.Fatal("unclassified error text leaked")
			}
		})
	}
}

func TestDefaultLoginCodeRequiresExistingModernFlow(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "packages"), 0700); err != nil {
		t.Fatal(err)
	}
	app, err := newTestServer(server.Config{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	httpServer := httptest.NewServer(app.Handler())
	defer httpServer.Close()
	jar, _ := cookiejar.New(nil)
	h := &harness{app: app, http: httpServer, client: &http.Client{Jar: jar}}
	var state wire.State
	h.request(t, "GET", "/api/state", nil, 200, &state)
	h.csrf = state.Csrf
	body := h.request(t, "POST", "/api/login", wire.LoginRequest{Email: "person@example.invalid", Password: "password-marker", Code: "123456"}, 401, nil)
	var response struct {
		Code string `json:"code"`
	}
	if err = json.Unmarshal(body, &response); err != nil || response.Code != "verification_expired" {
		t.Fatal("server did not use stateful modern authenticator", response, err)
	}
}
