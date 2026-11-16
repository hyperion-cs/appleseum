// Package fault names the error codes the API returns; the frontend shows its own text for each.
package fault

import (
	"errors"
	"syscall"
)

const (
	InvalidRequest        Code = "invalid_request"
	InvalidSelection      Code = "invalid_selection"
	SelectionTooLarge     Code = "selection_too_large"
	InvalidCredentials    Code = "invalid_credentials"
	InvalidIpa            Code = "invalid_ipa"
	InvalidDeviceSettings Code = "invalid_device_settings"

	SignInRequired                Code = "sign_in_required"
	SessionKeyRequired            Code = "session_key_required"
	SessionExpired                Code = "session_expired"
	TwoFactorRequired             Code = "two_factor_required"
	PasswordRejected              Code = "password_rejected"
	AccountCredentialsRejected    Code = "account_credentials_rejected"
	VerificationCodeRejected      Code = "verification_code_rejected"
	VerificationExpired           Code = "verification_expired"
	ReauthenticationFailed        Code = "reauthentication_failed"
	PurchaseHistoryAuthentication Code = "purchase_history_authentication_required"
	ProfileAuthenticationRequired Code = "profile_authentication_required"

	ReadOnly                     Code = "read_only"
	DeviceAuthenticationRequired Code = "device_authentication_required"
	NotFound                     Code = "not_found"

	Busy              Code = "busy"
	Canceled          Code = "canceled"
	ActionUnavailable Code = "action_unavailable"
	FilesInUse        Code = "files_in_use"
	SharedFiles       Code = "shared_files"
	SharingDuplicate  Code = "sharing_duplicate"
	SharingLimit      Code = "sharing_limit"
	MissingPackage    Code = "missing_package"
	VersionsChanged   Code = "versions_changed"
	ImportFailed      Code = "import_failed"

	ImportTooLarge Code = "import_too_large"
	RateLimited    Code = "rate_limited"
	ImportBusy     Code = "import_busy"

	StorageFull           Code = "storage_full"
	QueueFull             Code = "queue_full"
	FileTooLarge          Code = "file_too_large"
	DownloadTimeout       Code = "download_timeout"
	StorageFailed         Code = "storage_failed"
	LoginProcessingFailed Code = "login_processing_failed"

	AppleUnavailable              Code = "apple_unavailable"
	AppleResponseUnexpected       Code = "apple_response_unexpected"
	AppleDeviceVerificationFailed Code = "apple_device_verification_failed"
	AppleSignInDenied             Code = "apple_sign_in_denied"
	DeliveryVerificationFailed    Code = "delivery_verification_failed"

	ServerUnavailable Code = "server_unavailable"
)

// Codes stored with a job or a version row rather than returned by a request.
const (
	DownloadFailed     Code = "download_failed"
	InvalidPackage     Code = "invalid_package"
	Interrupted        Code = "interrupted"
	VersionUnavailable Code = "version_unavailable"
	ProfileUnavailable Code = "profile_unavailable"
)

// Code is an error that reaches the client as {"code": ...}.
type Code string

func (c Code) Error() string { return string(c) }

// Of returns the code an error carries; any other error is a storage failure.
func Of(err error) Code {
	// Preserve capacity failures even when a higher layer added a generic code.
	if errors.Is(err, syscall.ENOSPC) || errors.Is(err, syscall.EDQUOT) || nativeStorageFull(err) {
		return StorageFull
	}
	var sqlite interface{ Code() int }
	if errors.As(err, &sqlite) && sqlite.Code()&255 == 13 {
		return StorageFull
	}
	var code Code
	if errors.As(err, &code) {
		return code
	}
	return StorageFailed
}

// Is reports whether err carries one of the codes.
func Is(err error, codes ...Code) bool {
	var code Code
	if !errors.As(err, &code) {
		return false
	}
	for _, c := range codes {
		if c == code {
			return true
		}
	}
	return false
}
