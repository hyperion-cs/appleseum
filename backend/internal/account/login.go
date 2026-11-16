package account

import (
	"strings"
	"unicode"

	"github.com/hyperion-cs/appleseum/applekit"

	"appleseum/backend/internal/fault"
)

// NormalizeLogin folds an email to lower case and a phone number to its digits,
// so every spelling of one login finds the same device.
func NormalizeLogin(login string) string {
	login = strings.TrimSpace(login)
	if strings.Contains(login, "@") {
		return strings.ToLower(login)
	}
	phone := strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || strings.ContainsRune("().-−", r) || r >= '‐' && r <= '―' {
			return -1
		}
		return r
	}, login)
	digits := strings.TrimPrefix(phone, "+")
	if digits == "" || strings.Trim(digits, "0123456789") != "" {
		return login
	}
	// A Russian number dialed with the trunk prefix 8 is the same as +7.
	if !strings.HasPrefix(phone, "+") && len(digits) == 11 && digits[0] == '8' {
		digits = "7" + digits[1:]
	}
	return digits
}

// LoginFailure names why Apple refused a sign-in.
func LoginFailure(err error) fault.Code {
	if fault.Of(err) == fault.StorageFull {
		return fault.StorageFull
	}
	switch applekit.ClassifyLoginError(err) {
	case applekit.LoginTwoFactorRequired:
		return fault.TwoFactorRequired
	case applekit.LoginCredentialsRejected:
		return fault.AccountCredentialsRejected
	case applekit.LoginPasswordRejected:
		return fault.PasswordRejected
	case applekit.LoginVerificationCodeRejected:
		return fault.VerificationCodeRejected
	case applekit.LoginVerificationExpired:
		return fault.VerificationExpired
	case applekit.LoginStorageFailed:
		return fault.StorageFailed
	case applekit.LoginDeviceVerificationRequired:
		return fault.AppleDeviceVerificationFailed
	case applekit.LoginUnavailable:
		return fault.AppleUnavailable
	case applekit.LoginResponseUnexpected:
		return fault.AppleResponseUnexpected
	case applekit.LoginDenied:
		return fault.AppleSignInDenied
	default:
		return fault.LoginProcessingFailed
	}
}
