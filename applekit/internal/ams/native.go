package ams

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"howett.net/plist"

	"github.com/hyperion-cs/appleseum/applekit/internal/adi"
	"github.com/hyperion-cs/appleseum/applekit/internal/httpx"
	"github.com/hyperion-cs/appleseum/applekit/internal/plistutil"
)

const (
	maxNativeResponse     = 4_194_304
	nativeSignatureLength = 501
)

// ActionSigner signs the exact serialized plist sent to native authentication.
type ActionSigner interface {
	Sign([]byte) ([]byte, error)
}

// MD fields come from GrandSlam CPD; AMD fields come from the AMD controller.
type NativeDevice struct {
	LocalUser    string
	Md           string
	MachineId    string
	Routing      string
	Amd          string
	AmdMachineId string
}

type NativeCredentials struct {
	AppleId             string
	PasswordEquivalent  string
	Adsid               string
	DirectoryServicesId string
	Guid                string
}

type NativeOptions struct {
	Context      RequestContext
	Locale       string
	TimeZone     string
	ComplianceId string
	ClientTime   time.Time
	// With a controller, AMD headers come from its provider; NativeDevice's AMD fields must be empty.
	Amd *AmdController
}

type NativeSession struct {
	PasswordToken       string
	DirectoryServicesId string
	AlternateDsid       string
	Storefront          string
	Pod                 string
}

// NativeError contains allowlisted diagnostics only, never tokens or response text.
type NativeError struct {
	Status     int
	Reason     string
	ResultCode *int64
}

func (e *NativeError) Error() string {
	message := fmt.Sprintf("AMS native authentication failed: status=%d reason=%s", e.Status, e.Reason)
	if e.ResultCode != nil {
		message += fmt.Sprintf(" result=%d", *e.ResultCode)
	}
	return message
}

func AuthenticateNative(ctx context.Context, client *http.Client, endpoint string, signer ActionSigner, credentials NativeCredentials, device NativeDevice, options NativeOptions) (NativeSession, error) {
	if client == nil || client.Jar == nil || signer == nil {
		return NativeSession{}, errors.New("AMS native authentication requires HTTP client and signer")
	}
	if err := validateNativeEndpoint(endpoint); err != nil {
		return NativeSession{}, err
	}
	headers, err := nativeHeaders(credentials, device, options)
	if err != nil {
		return NativeSession{}, err
	}
	body, err := plist.Marshal(map[string]any{
		"appleId":                credentials.AppleId,
		"createSession":          true,
		"credentialSource":       uint64(2),
		"guid":                   credentials.Guid,
		"isSilentAuthentication": false,
		"password":               credentials.PasswordEquivalent,
		"passwordSettings":       map[string]any{"free": "always", "paid": "always"},
		"rmp":                    "0",
	}, plist.XMLFormat)
	if err != nil {
		return NativeSession{}, fmt.Errorf("encode AMS native request: %w", err)
	}
	defer clear(body)
	requestContext := httpx.WithProfile(ctx, httpx.NativeAuthentication)
	nativeClient := httpx.NoRedirect(client)
	for attempt := 0; attempt < 6; attempt++ {
		amd, machine := device.Amd, device.AmdMachineId
		if options.Amd != nil {
			amd, machine, err = options.Amd.Headers(options.ClientTime)
			if err != nil && !errors.Is(err, adi.ErrAmdNotProvisioned) {
				return NativeSession{}, fmt.Errorf("generate AMS AMD headers: %w", err)
			}
			// Omit unprovisioned AMD headers: Apple may answer with SP before authentication.
		}
		signature, err := signer.Sign(body)
		if err != nil {
			return NativeSession{}, fmt.Errorf("sign AMS native request: %w", err)
		}
		if len(signature) != nativeSignatureLength {
			clear(signature)
			return NativeSession{}, errors.New("invalid AMS native action signature")
		}

		request, err := http.NewRequestWithContext(requestContext, http.MethodPost, endpoint, bytes.NewReader(body))
		if err != nil {
			clear(signature)
			return NativeSession{}, errors.New("create AMS native request")
		}
		request.Header = headers
		request.Header.Set("X-Apple-ActionSignature", base64.StdEncoding.EncodeToString(signature))
		clear(signature)
		if amd != "" {
			request.Header.Set("X-Apple-AMD", amd)
			request.Header.Set("X-Apple-AMD-M", machine)
		}
		response, err := nativeClient.Do(request)
		if err != nil {
			return NativeSession{}, fmt.Errorf("AMS native exchange: %w", err)
		}
		raw, readErr := httpx.ReadResponseBody(response, maxNativeResponse)
		response.Body.Close()
		if readErr != nil {
			return NativeSession{}, fmt.Errorf("read AMS native response: %w", readErr)
		}
		if options.Amd != nil {
			if attempt == 5 && response.Header.Get("X-Apple-AMD-Action") != "" {
				clear(raw)
				return NativeSession{}, &NativeError{Status: response.StatusCode, Reason: "amd_action_limit"}
			}
			handled, actionErr := options.Amd.HandleAction(ctx, client, options.Context, response.Header, credentials.Guid, options.ClientTime)
			if actionErr != nil {
				clear(raw)
				return NativeSession{}, fmt.Errorf("handle AMS AMD action: %w", actionErr)
			}
			if handled {
				clear(raw)
				// Let the cookie jar and AMD provider supply fresh values for the retry.
				headers = headers.Clone()
				headers.Del("Cookie")
				headers.Del("X-Apple-AMD")
				headers.Del("X-Apple-AMD-M")
				continue
			}
		}
		session, err := decodeNativeSession(response, raw, credentials.DirectoryServicesId, options.Context.Storefront)
		clear(raw)
		return session, err
	}
	return NativeSession{}, &NativeError{Status: http.StatusOK, Reason: "amd_action_limit"}
}

func decodeNativeSession(response *http.Response, raw []byte, expectedDsid, initialStorefront string) (NativeSession, error) {
	if response.StatusCode != http.StatusOK {
		return NativeSession{}, &NativeError{Status: response.StatusCode, Reason: "upstream_http"}
	}
	var result struct {
		Status        any    `plist:"status"`
		PasswordToken string `plist:"passwordToken"`
		Dsid          string `plist:"dsPersonId"`
		AlternateDsid string `plist:"altDsid"`
		Allowed       any    `plist:"m-allowed"`
		Action        any    `plist:"action"`
	}
	_, decodeErr := plistutil.Unmarshal(raw, &result)
	if decodeErr != nil {
		return NativeSession{}, &NativeError{Status: response.StatusCode, Reason: "invalid_response"}
	}
	status, ok := plistutil.Int64(result.Status)
	if !ok {
		return NativeSession{}, &NativeError{Status: response.StatusCode, Reason: "invalid_response"}
	}
	storefront := response.Header.Get("X-Set-Apple-Store-Front")
	action, _ := result.Action.(map[string]any)
	allowed, _ := result.Allowed.(bool)
	// -128 can accompany a valid session when SetUserAndReset changes the storefront.
	// Require the observed reset action, allowed flag and complete matching credentials.
	storefrontReset := status == -128 && action["kind"] == "SetUserAndReset" &&
		allowed && result.PasswordToken != "" && result.Dsid == expectedDsid &&
		storefrontPattern.MatchString(storefront) && storefront[:6] != initialStorefront[:6]
	if status != 0 && !storefrontReset {
		return NativeSession{}, &NativeError{Status: response.StatusCode, Reason: "unrecognized_result", ResultCode: &status}
	}
	if result.PasswordToken == "" || result.Dsid == "" {
		return NativeSession{}, &NativeError{Status: response.StatusCode, Reason: "missing_session"}
	}
	if result.Dsid != expectedDsid {
		return NativeSession{}, &NativeError{Status: response.StatusCode, Reason: "identity_mismatch"}
	}
	if storefront == "" {
		storefront = response.Header.Get("X-Apple-Request-Store-Front")
	}
	return NativeSession{
		PasswordToken: result.PasswordToken, DirectoryServicesId: result.Dsid,
		AlternateDsid: result.AlternateDsid,
		Storefront:    storefront, Pod: response.Header.Get("Pod"),
	}, nil
}

func validateNativeEndpoint(value string) error {
	endpoint, err := url.Parse(value)
	if err != nil || endpoint.Scheme != "https" || endpoint.User != nil || endpoint.Host != "auth.itunes.apple.com" || endpoint.Path != "/auth/v1/native" || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return errors.New("invalid AMS native endpoint")
	}
	return nil
}

func nativeHeaders(credentials NativeCredentials, device NativeDevice, options NativeOptions) (http.Header, error) {
	if options.Context.Actor != Configurator {
		return nil, errors.New("AMS native authentication requires Configurator context")
	}
	headers, err := options.Context.Headers()
	if err != nil {
		return nil, err
	}
	values := []string{
		credentials.AppleId, credentials.PasswordEquivalent, credentials.Adsid,
		credentials.DirectoryServicesId, credentials.Guid, device.LocalUser,
		device.Md, device.MachineId, device.Routing,
		options.Locale, options.TimeZone, options.ComplianceId,
	}
	if options.Amd == nil {
		values = append(values, device.Amd, device.AmdMachineId)
	} else if device.Amd != "" || device.AmdMachineId != "" || options.Amd.Provider == nil {
		return nil, errors.New("invalid AMS AMD configuration")
	}
	for _, value := range values {
		if value == "" || len(value) > 16_384 || strings.TrimSpace(value) != value || strings.ContainsAny(value, "\r\n\x00") {
			return nil, errors.New("invalid AMS native input")
		}
	}
	if options.ClientTime.IsZero() || len(options.ComplianceId) != 2 || options.ComplianceId[0] < 'A' || options.ComplianceId[0] > 'Z' || options.ComplianceId[1] < 'A' || options.ComplianceId[1] > 'Z' {
		return nil, errors.New("invalid AMS native input")
	}
	headers.Set("Content-Type", "application/x-apple-plist")
	headers.Set("X-Apple-ADSID", credentials.Adsid)
	headers.Set("X-DSID", credentials.DirectoryServicesId)
	headers.Set("ICloud-DSID", credentials.DirectoryServicesId)
	headers.Set("X-Apple-I-MD", device.Md)
	headers.Set("X-Apple-I-MD-M", device.MachineId)
	headers.Set("X-Apple-I-MD-LU", device.LocalUser)
	headers.Set("X-Apple-I-MD-RINFO", device.Routing)
	headers.Set("X-Apple-I-Client-Time", options.ClientTime.UTC().Format(time.RFC3339))
	headers.Set("X-Apple-I-Locale", options.Locale)
	headers.Set("X-Apple-I-TimeZone", options.TimeZone)
	headers.Set("X-Apple-Age-Area-Compliance-ID", options.ComplianceId)
	return headers, nil
}
