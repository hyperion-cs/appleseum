package applekit

import (
	"errors"
	"net/http"
)

const (
	LoginProcessingFailed LoginFailure = iota
	LoginTwoFactorRequired
	LoginCredentialsRejected
	LoginPasswordRejected
	LoginVerificationCodeRejected
	LoginVerificationExpired
	LoginStorageFailed
	LoginDeviceVerificationRequired
	LoginUnavailable
	LoginResponseUnexpected
	LoginDenied
)

// LoginFailure classifies a sign-in failure independently of an application's
// HTTP status codes, messages and persistence policy.
type LoginFailure int

// ClassifyLoginError interprets sign-in diagnostics and wrapped sentinel errors.
// A specific diagnostic takes precedence over a broader credentials rejection.
// Unknown errors, including nil, return LoginProcessingFailed.
func ClassifyLoginError(err error) LoginFailure {
	var diagnostic *LoginError
	errors.As(err, &diagnostic)
	switch {
	case errors.Is(err, ErrTwoFactorRequired):
		return LoginTwoFactorRequired
	case diagnostic != nil && diagnostic.Reason == "account_credentials_rejected":
		return LoginCredentialsRejected
	case diagnostic != nil && diagnostic.Reason == "password_rejected":
		return LoginPasswordRejected
	case errors.Is(err, ErrPasswordRejected):
		return LoginPasswordRejected
	case errors.Is(err, ErrCredentialsRejected):
		return LoginCredentialsRejected
	case errors.Is(err, ErrVerificationCodeRejected):
		return LoginVerificationCodeRejected
	case errors.Is(err, ErrVerificationExpired):
		return LoginVerificationExpired
	case diagnostic == nil:
		return LoginProcessingFailed
	case diagnostic.Stage == "device_storage":
		return LoginStorageFailed
	}
	switch diagnostic.Reason {
	case "verification_code_rejected":
		return LoginVerificationCodeRejected
	case "device_verification_required":
		return LoginDeviceVerificationRequired
	case "dns", "network", "timeout", "certificate":
		return LoginUnavailable
	case "unrecognized_result", "invalid_response", "missing_session", "identity_mismatch", "amd_action_limit":
		return LoginResponseUnexpected
	case "upstream_http":
		if diagnostic.Status == http.StatusForbidden {
			return LoginDenied
		}
		return LoginUnavailable
	}
	return LoginProcessingFailed
}

// IsPurchaseHistoryAuthentication identifies a refusal scoped to purchase
// history. It does not by itself establish that the whole store session expired.
func IsPurchaseHistoryAuthentication(err error) bool {
	var rejected *SessionRejection
	return errors.As(err, &rejected) && rejected.Operation == "purchases" && rejected.Reason == "authentication"
}
