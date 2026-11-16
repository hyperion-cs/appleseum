package grandslam

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/hyperion-cs/appleseum/applekit/internal/httpx"
	"github.com/hyperion-cs/appleseum/applekit/internal/plistutil"
)

// Success acknowledges the code request, not delivery or completed login.
func (s Service) RequestTrustedDeviceCode(ctx context.Context, session SessionData, cpd map[string]any) error {
	return s.trustedDevice(ctx, session, cpd, "")
}

// After code verification, repeat SRP with the same identity to obtain service tokens.
func (s Service) VerifyTrustedDeviceCode(ctx context.Context, session SessionData, cpd map[string]any, code string) error {
	if len(code) != 6 {
		return errors.New("grandslam: six-digit verification code required")
	}
	for _, c := range code {
		if c < '0' || c > '9' {
			return errors.New("grandslam: six-digit verification code required")
		}
	}
	return s.trustedDevice(ctx, session, cpd, code)
}

func (s Service) trustedDevice(ctx context.Context, session SessionData, cpd map[string]any, code string) error {
	if s.Http == nil || s.Http.Jar == nil ||
		strings.TrimSpace(session.Adsid) == "" || len(session.Adsid) > 1_024 || strings.Contains(session.Adsid, ":") ||
		strings.TrimSpace(session.Idms) == "" || len(session.Idms) > 16_384 {
		return errors.New("grandslam: shared HTTP client and verified identity session required")
	}
	headers, err := s.headers(cpd)
	if err != nil {
		return err
	}
	clientTime, _ := cpd["X-Apple-I-Client-Time"].(string)
	if _, err := time.Parse(time.RFC3339, clientTime); err != nil {
		return errors.New("grandslam: device snapshot time required")
	}
	headers.Set("X-Apple-I-Client-Time", clientTime)
	headers.Set("Accept", "text/x-xml-plist")
	headers.Set("X-Apple-Identity-Token", base64.StdEncoding.EncodeToString([]byte(session.Adsid+":"+session.Idms)))
	for _, key := range []string{"X-Apple-Locale", "X-Apple-I-TimeZone"} {
		if value, present := cpd[key]; present {
			text, ok := value.(string)
			if !ok || len(text) == 0 || len(text) > 100 {
				return errors.New("grandslam: invalid device locale or timezone")
			}
			for _, c := range text {
				if c < ' ' || c > '~' {
					return errors.New("grandslam: invalid device locale or timezone")
				}
			}
			headers.Set(key, text)
		}
	}
	endpoint := "https://gsa.apple.com/auth/verify/trusteddevice"
	if code != "" {
		endpoint = "https://gsa.apple.com/grandslam/GsService2/validate"
		headers.Set("Security-Code", code)
	}
	req, err := http.NewRequestWithContext(httpx.WithProfile(ctx, httpx.AuthKit), http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header = headers
	response, err := httpx.NoRedirect(s.Http).Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	raw, err := httpx.ReadResponseBody(response, maxServiceBody)
	if err != nil {
		return err
	}
	defer clear(raw)
	var status map[string]any
	_, decodeErr := plistutil.Unmarshal(raw, &status)
	ec, validStatus := plistutil.Int64(status["ec"])
	// Preserve explicit body diagnostics even when HTTP is also an error.
	if decodeErr == nil && validStatus && ec != 0 {
		return &StatusError{HttpStatus: response.StatusCode, Status: Status{Code: ec}}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return &HttpError{StatusCode: response.StatusCode}
	}
	if code == "" {
		return nil
	} // Successful delivery can return an HTML form.
	if decodeErr != nil {
		return errors.New("grandslam: invalid verification response plist")
	}
	if !validStatus {
		return errors.New("grandslam: missing or invalid verification status")
	}

	return nil
}
