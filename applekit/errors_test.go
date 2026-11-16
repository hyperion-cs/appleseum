package applekit

import (
	"errors"
	"fmt"
	"testing"
)

func TestClassifyLoginError(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want LoginFailure
	}{
		{"two factor", ErrTwoFactorRequired, LoginTwoFactorRequired},
		{"credentials", ErrCredentialsRejected, LoginCredentialsRejected},
		{"password", ErrPasswordRejected, LoginPasswordRejected},
		{"code", ErrVerificationCodeRejected, LoginVerificationCodeRejected},
		{"expired code", ErrVerificationExpired, LoginVerificationExpired},
		{"specific password", errors.Join(&LoginError{Reason: "password_rejected"}, ErrCredentialsRejected), LoginPasswordRejected},
		{"specific credentials", errors.Join(&LoginError{Reason: "account_credentials_rejected"}, ErrPasswordRejected), LoginCredentialsRejected},
		{"pending verification", errors.Join(&LoginError{Reason: "password_rejected"}, ErrTwoFactorRequired), LoginTwoFactorRequired},
		{"storage", &LoginError{Stage: "device_storage", Reason: "protocol"}, LoginStorageFailed},
		{"storage before network", &LoginError{Stage: "device_storage", Reason: "network"}, LoginStorageFailed},
		{"verification diagnostic", &LoginError{Reason: "verification_code_rejected"}, LoginVerificationCodeRejected},
		{"device challenge", &LoginError{Reason: "device_verification_required"}, LoginDeviceVerificationRequired},
		{"dns", &LoginError{Reason: "dns"}, LoginUnavailable},
		{"network", &LoginError{Reason: "network"}, LoginUnavailable},
		{"timeout", &LoginError{Reason: "timeout"}, LoginUnavailable},
		{"certificate", &LoginError{Reason: "certificate"}, LoginUnavailable},
		{"unknown result", &LoginError{Reason: "unrecognized_result"}, LoginResponseUnexpected},
		{"invalid response", &LoginError{Reason: "invalid_response"}, LoginResponseUnexpected},
		{"missing session", &LoginError{Reason: "missing_session"}, LoginResponseUnexpected},
		{"identity mismatch", &LoginError{Reason: "identity_mismatch"}, LoginResponseUnexpected},
		{"action limit", &LoginError{Reason: "amd_action_limit"}, LoginResponseUnexpected},
		{"denied", &LoginError{Reason: "upstream_http", Status: 403}, LoginDenied},
		{"unavailable", &LoginError{Reason: "upstream_http", Status: 503}, LoginUnavailable},
		{"protocol", &LoginError{Reason: "protocol"}, LoginProcessingFailed},
		{"future diagnostic", &LoginError{Reason: "future-reason"}, LoginProcessingFailed},
		{"unknown error", errors.New("unclassified"), LoginProcessingFailed},
		{"nil", nil, LoginProcessingFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifyLoginError(tc.err); got != tc.want {
				t.Fatalf("classification=%v, want %v", got, tc.want)
			}
			if tc.err != nil {
				if got := ClassifyLoginError(fmt.Errorf("wrapped: %w", tc.err)); got != tc.want {
					t.Fatalf("wrapped classification=%v, want %v", got, tc.want)
				}
			}
		})
	}
}

func TestPurchaseHistoryAuthenticationScope(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want bool
	}{
		{&SessionRejection{Operation: "purchases", Reason: "authentication", Status: 401}, true},
		{&SessionRejection{Operation: "purchases", Reason: "authentication", Status: 403}, true},
		{&SessionRejection{Operation: "purchases", Reason: "token_expired"}, false},
		{&SessionRejection{Operation: "profile", Reason: "authentication"}, false},
		{&SessionRejection{Operation: "download", Reason: "authentication"}, false},
		{ErrSessionExpired, false},
		{errors.New("purchases/authentication"), false},
		{nil, false},
	} {
		if got := IsPurchaseHistoryAuthentication(tc.err); got != tc.want {
			t.Fatalf("scope of %v=%v, want %v", tc.err, got, tc.want)
		}
		if tc.err != nil && IsPurchaseHistoryAuthentication(fmt.Errorf("wrapped: %w", tc.err)) != tc.want {
			t.Fatalf("lost scope through wrapper: %v", tc.err)
		}
	}
}
