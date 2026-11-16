package storekit

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/hyperion-cs/appleseum/applekit/internal/ams"
	"github.com/hyperion-cs/appleseum/applekit/internal/grandslam"
	"github.com/hyperion-cs/appleseum/applekit/internal/httpx"
)

func TestModernLoginDiagnosticsPreserveServerResults(t *testing.T) {
	status := &grandslam.StatusError{HttpStatus: 200, Status: grandslam.Status{Code: -22_421, ServerStatus: 434, Message: "private-server-text", DeviceData: "private-device-data"}}
	diagnostic := NewLoginError("grandslam", fmt.Errorf("outer: %w", status))
	var original *grandslam.StatusError
	if diagnostic.Status != 200 || diagnostic.Failure != "-22421" || diagnostic.Message != "hsc_434" || !errors.As(diagnostic, &original) || original != status {
		t.Fatal("lost inner result")
	}
	if strings.Contains(diagnostic.Error(), "private-") {
		t.Fatal("private payload escaped default diagnostics")
	}
	code := int64(-128)
	native := &ams.NativeError{Status: 200, Reason: "unrecognized_result", ResultCode: &code}
	wrapped := NewLoginError("native", fmt.Errorf("outer: %w", native))
	if wrapped.Failure != "-128" || wrapped.Status != 200 || !errors.Is(wrapped, native) || !strings.Contains(wrapped.Error(), "failure=-128") {
		t.Fatal("native result code lost in diagnostics", wrapped)
	}
	for _, tc := range []struct {
		err    error
		reason string
		status int
	}{
		{context.Canceled, "canceled", 0}, {context.DeadlineExceeded, "timeout", 0},
		{&grandslam.HttpError{StatusCode: 503}, "upstream_http", 503},
		{fmt.Errorf("SAP wrapper: %w", &httpx.HttpError{StatusCode: 403}), "upstream_http", 403},
		{&ams.NativeError{Status: 200, Reason: "invalid_response"}, "invalid_response", 200},
	} {
		got := NewLoginError("native", tc.err)
		if got.Reason != tc.reason || got.Status != tc.status || !errors.Is(got, tc.err) {
			t.Fatal(got)
		}
	}
}

func TestLoginRejectionsDistinguishPasswordCodeDeviceAndUnknown(t *testing.T) {
	for _, tc := range []struct {
		stage     string
		code      int64
		challenge bool
		want      string
	}{
		{"grandslam", -22_406, false, "password_rejected"},
		{"grandslam", -20_970, false, "account_credentials_rejected"},
		{"grandslam", -20_101, false, "account_credentials_rejected"},
		{"verification", -21_669, false, "verification_code_rejected"},
		{"grandslam", -21_669, false, "unrecognized_result"},
		{"verification", -22_406, false, "unrecognized_result"},
		{"grandslam", -22_421, true, "device_verification_required"},
		{"grandslam", -22_406, true, "device_verification_required"},
		{"grandslam", -20_970, true, "device_verification_required"},
		{"grandslam", -20_101, true, "device_verification_required"},
		{"verification", -20_101, false, "unrecognized_result"},
		{"grandslam", -99_999, false, "unrecognized_result"},
	} {
		status := &grandslam.StatusError{HttpStatus: 200, Status: grandslam.Status{Code: tc.code}}
		if tc.challenge {
			status.ServerStatus = 434
			status.DeviceData = "private-sim"
		}
		if got := NewLoginError(tc.stage, status); got.Reason != tc.want {
			t.Fatalf("stage %s ec %d: %s", tc.stage, tc.code, got.Reason)
		}
		if tc.challenge && errors.Is(status, grandslam.ErrCredentialsRejected) {
			t.Fatal("device challenge classified as a credentials rejection")
		}
		if errors.Is(status, grandslam.ErrPasswordRejected) != (tc.code == -22_406 && !tc.challenge) {
			t.Fatal("wrong password rejection classification")
		}
	}
}
