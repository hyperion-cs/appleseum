package storekit

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"net"

	"github.com/hyperion-cs/appleseum/applekit/internal/ams"
	"github.com/hyperion-cs/appleseum/applekit/internal/grandslam"
	"github.com/hyperion-cs/appleseum/applekit/internal/httpx"
)

// LoginError must contain only allowlisted metadata, never free-form server text or secrets.
type LoginError struct {
	Stage      string
	Reason     string
	Status     int
	Failure    string
	Message    string
	BodyFormat string
	BodyBytes  int
	cause      error
}

func (e *LoginError) Error() string {
	text := fmt.Sprintf("stage=%s reason=%s status=%d failure=%s message=%s", e.Stage, e.Reason, e.Status, e.Failure, e.Message)
	if e.BodyFormat != "" {
		text += fmt.Sprintf(" body=%s bytes=%d", e.BodyFormat, e.BodyBytes)
	}
	return text
}

func (e *LoginError) Unwrap() error { return e.cause }

func loginErrorReason(err error) string {
	var dns *net.DNSError
	var cert *x509.UnknownAuthorityError
	var network net.Error
	switch {
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.As(err, &dns):
		return "dns"
	case errors.As(err, &cert):
		return "certificate"
	case errors.As(err, &network):
		if network.Timeout() {
			return "timeout"
		}
		return "network"
	default:
		return "protocol"
	}
}

func NewLoginError(stage string, err error) *LoginError {
	result := &LoginError{Stage: stage, Reason: loginErrorReason(err), cause: err}
	var status *grandslam.StatusError
	var httpError *httpx.HttpError
	var native *ams.NativeError
	switch {
	case errors.As(err, &status):
		result.Status = status.HttpStatus
		result.Failure = fmt.Sprint(status.Code)
		result.Message = fmt.Sprintf("hsc_%d", status.ServerStatus)
		result.Reason = "unrecognized_result"
	case errors.As(err, &httpError):
		result.Status = httpError.StatusCode
		result.Reason = "upstream_http"
	case errors.As(err, &native):
		result.Status = native.Status
		result.Reason = native.Reason
		if native.ResultCode != nil {
			result.Failure = fmt.Sprint(*native.ResultCode)
		}
	}
	if errors.As(err, &status) {
		switch {
		case status.DeviceData != "" || status.ServerStatus == 434:
			result.Reason = "device_verification_required"
		case stage == "grandslam" && (status.Code == -20_970 || status.Code == -20_101):
			result.Reason = "account_credentials_rejected"
		case stage == "grandslam" && errors.Is(err, grandslam.ErrPasswordRejected):
			result.Reason = "password_rejected"
		case stage == "verification" && errors.Is(err, grandslam.ErrVerificationCodeRejected):
			result.Reason = "verification_code_rejected"
		}
	}
	return result
}
