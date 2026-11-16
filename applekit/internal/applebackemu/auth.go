package applebackemu

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"fmt"
	"net/http"
	"reflect"

	"howett.net/plist"
)

const (
	grandSlamUrl = "https://gsa.apple.com/grandslam/GsService2"
	authKitAgent = "akd/1.0 CFNetwork/3860.600.21 Darwin/25.5.0"
)

// AuthDevice is the expected device snapshot, independent of the client types.
type AuthDevice struct {
	ClientInfo string
	Language   string
	ClientData map[string]any
}

// AuthAccount contains only synthetic credentials and issued session values.
type AuthAccount struct {
	AppleId, Password, Adsid, DirectoryServicesId, Idms, Pet string
}

// PlistResponse encodes server data without adding an envelope.
func PlistResponse(fields map[string]any) (Response, error) {
	body, err := plist.Marshal(fields, plist.XMLFormat)
	return Response{Header: http.Header{"Content-Type": {"text/x-xml-plist"}}, Body: body}, err
}

func authHeaders(device AuthDevice) http.Header {
	h := http.Header{}
	for name, value := range map[string]string{
		"User-Agent": authKitAgent, "Content-Type": "text/x-xml-plist", "Accept": "*/*",
		"Accept-Encoding": "gzip, deflate, br", "Accept-Language": device.Language,
		"X-MMe-Client-Info": device.ClientInfo, "X-Apple-AK-Context-Type": "itunesstore",
		"X-Apple-I-Auth-Type": "2", "X-Apple-Client-App-Name": "Apple Configurator",
		"X-Apple-I-Client-Bundle-Id": "com.apple.configurator.ui",
	} {
		h.Set(name, value)
	}
	for _, name := range []string{"X-Apple-I-MD", "X-Apple-I-MD-M", "X-Apple-I-MD-LU", "X-Apple-I-MD-RINFO", "X-Mme-Device-Id"} {
		value, _ := device.ClientData[name].(string)
		h.Set(name, value)
	}
	return h
}

// GrandSlamRequest checks init/complete fields, CPD and envelope.
// Fields supplies expected A2k, M1 or c values; otherwise only types and lengths are checked.
func GrandSlamRequest(device AuthDevice, username, operation string, fields map[string]any, response Response) (Step, error) {
	if username == "" || (operation != "init" && operation != "complete") {
		return Step{}, fmt.Errorf("applebackemu: invalid GrandSlam identity or operation")
	}
	cpd, err := normalizedPlist(device.ClientData)
	if err != nil {
		return Step{}, err
	}
	expected, err := normalizedPlist(fields)
	if err != nil {
		return Step{}, err
	}
	step := Step{Name: "GrandSlam " + operation, Method: "POST", Url: grandSlamUrl, Header: authHeaders(device), Response: response}

	step.Check = func(request Request) error {
		var envelope map[string]any
		if _, err := plist.Unmarshal(request.Body, &envelope); err != nil {
			return fmt.Errorf("invalid GrandSlam plist")
		}
		header, _ := envelope["Header"].(map[string]any)
		body, _ := envelope["Request"].(map[string]any)
		if len(envelope) != 2 || len(header) != 1 || header["Version"] != "1.0.1" || len(body) != 5 || body["o"] != operation || body["u"] != username || !reflect.DeepEqual(body["cpd"], cpd) {
			return fmt.Errorf("GrandSlam envelope, identity or CPD mismatch")
		}
		if operation == "init" {
			public, _ := body["A2k"].([]byte)
			if len(public) == 0 || len(public) > 256 || !reflect.DeepEqual(body["ps"], []any{"s2k", "s2k_fo"}) {
				return fmt.Errorf("invalid GrandSlam public value or protocol list")
			}
		} else {
			proof, _ := body["M1"].([]byte)
			context, _ := body["c"].(string)
			if len(proof) != 32 || context == "" {
				return fmt.Errorf("invalid GrandSlam proof or context")
			}
		}
		for name, value := range expected {
			if !reflect.DeepEqual(body[name], value) {
				return fmt.Errorf("GrandSlam field %s mismatch", name)
			}
		}
		return nil
	}

	return step, nil
}

func normalizedPlist(fields map[string]any) (map[string]any, error) {
	if fields == nil {
		return nil, nil
	}
	encoded, err := plist.Marshal(fields, plist.XMLFormat)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	_, err = plist.Unmarshal(encoded, &result)
	return result, err
}

type TrustedDeviceOptions struct {
	Device            AuthDevice
	Adsid, Idms, Code string
}

// TrustedDeviceDelivery builds only the delivery stage for focused tests.
func TrustedDeviceDelivery(options TrustedDeviceOptions) (Step, error) {
	steps, err := TrustedDevice(options)
	if err != nil {
		return Step{}, err
	}
	return steps[0], nil
}

// TrustedDeviceVerification checks confirmation without requiring a delivery cookie.
// TrustedDevice binds delivery and confirmation.
func TrustedDeviceVerification(options TrustedDeviceOptions) (Step, error) {
	steps, err := TrustedDevice(options)
	if err != nil {
		return Step{}, err
	}
	steps[1].Check = nil
	return steps[1], nil
}

// TrustedDevice checks code delivery and cookie-bound confirmation.
// Each step's response can be overridden.
func TrustedDevice(options TrustedDeviceOptions) ([]Step, error) {
	if options.Adsid == "" || options.Idms == "" || len(options.Code) != 6 {
		return nil, fmt.Errorf("applebackemu: identity and six-digit code required")
	}
	for _, c := range options.Code {
		if c < '0' || c > '9' {
			return nil, fmt.Errorf("applebackemu: invalid verification code")
		}
	}
	header := authHeaders(options.Device)
	header.Set("Accept", "text/x-xml-plist")
	header.Set("X-Apple-Identity-Token", base64.StdEncoding.EncodeToString([]byte(options.Adsid+":"+options.Idms)))
	for _, name := range []string{"X-Apple-I-Client-Time", "X-Apple-Locale", "X-Apple-I-TimeZone"} {
		if value, ok := options.Device.ClientData[name].(string); ok {
			header.Set(name, value)
		}
	}
	deliveryHeader, verifyHeader := header.Clone(), header.Clone()
	deliveryHeader["Security-Code"] = nil
	verifyHeader.Set("Security-Code", options.Code)
	confirmation, err := PlistResponse(map[string]any{"ec": 0})
	if err != nil {
		return nil, err
	}
	confirmation.Header.Set("Set-Cookie", "synthetic-factor=verified; Path=/; Secure")
	return []Step{
		{Name: "request trusted-device code", Method: "GET", Url: "https://gsa.apple.com/auth/verify/trusteddevice", Header: deliveryHeader, Body: []byte{}, Response: Response{
			Header: http.Header{"Content-Type": {"text/html"}, "Set-Cookie": {"synthetic-factor=pending; Path=/; Secure"}}, Body: []byte("<html>synthetic delivery form</html>"),
		}},
		{Name: "verify trusted-device code", Method: "GET", Url: "https://gsa.apple.com/grandslam/GsService2/validate", Header: verifyHeader, Body: []byte{}, Response: confirmation,
			Check: func(request Request) error { return requireCookie(request, "synthetic-factor", "pending") }},
	}, nil
}

func requireCookie(request Request, name, value string) error {
	r := http.Request{Header: request.Header}
	cookie, err := r.Cookie(name)
	if err != nil || cookie.Value != value {
		return fmt.Errorf("missing or incorrect %s cookie", name)
	}
	return nil
}

// GzipResponse compresses a static synthetic response and infers its new length.
func GzipResponse(response Response) (Response, error) {
	if response.OpenBody != nil {
		return Response{}, fmt.Errorf("applebackemu: gzip helper requires a static body")
	}
	var compressed bytes.Buffer
	w := gzip.NewWriter(&compressed)
	if _, err := w.Write(response.Body); err != nil {
		return Response{}, err
	}
	if err := w.Close(); err != nil {
		return Response{}, err
	}
	response.Header = response.Header.Clone()
	if response.Header == nil {
		response.Header = make(http.Header)
	}
	response.Header.Set("Content-Encoding", "gzip")
	response.ContentLength = nil
	response.Body = compressed.Bytes()
	return response, nil
}
