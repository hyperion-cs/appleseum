package applebackemu

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"reflect"

	"howett.net/plist"
)

type NativeOptions struct {
	Account                                       AuthAccount
	Guid                                          string
	Header                                        http.Header // Additional expected device, locale, AMD and cookie headers.
	Signature                                     []byte      // Nil checks only the base64 encoding and 501-byte length.
	PasswordToken, AlternateDsid, Storefront, Pod string
}

// NativeAuthentication checks PET login and returns a synthetic store session.
// Tests supply the SAP signer; this scenario does not verify its cryptography.
func NativeAuthentication(options NativeOptions) (Step, error) {
	a := options.Account
	if a.AppleId == "" || a.Pet == "" || a.DirectoryServicesId == "" || options.Guid == "" {
		return Step{}, fmt.Errorf("applebackemu: incomplete native identity")
	}
	expected, err := normalizedPlist(map[string]any{
		"appleId": a.AppleId, "password": a.Pet, "guid": options.Guid,
		"credentialSource": 2, "createSession": true, "isSilentAuthentication": false,
		"passwordSettings": map[string]any{"free": "always", "paid": "always"}, "rmp": "0",
	})
	if err != nil {
		return Step{}, err
	}
	header := http.Header{
		"User-Agent":   {"Configurator/2.21 Configurator/2.21 (Macintosh; OS X 26.5; 25F71) AppleWebKit/1624.2.5.11.4 AMS/1 (dt:1)"},
		"Content-Type": {"application/x-apple-plist"}, "Accept": {"*/*"},
		"Accept-Encoding": {"br, gzip, deflate"}, "Priority": {"u=3, i"},
		"X-Apple-Client-Application": {"com.apple.configurator.ui"},
		"X-Apple-ADSID":              {a.Adsid}, "X-DSID": {a.DirectoryServicesId}, "ICloud-DSID": {a.DirectoryServicesId},
	}
	for name, values := range options.Header {
		header[http.CanonicalHeaderKey(name)] = append([]string(nil), values...)
	}
	if options.Signature != nil {
		header.Set("X-Apple-ActionSignature", base64.StdEncoding.EncodeToString(options.Signature))
	}
	response, err := PlistResponse(map[string]any{
		"status": 0, "passwordToken": options.PasswordToken, "dsPersonId": a.DirectoryServicesId, "altDsid": options.AlternateDsid,
	})
	if err != nil {
		return Step{}, err
	}
	if options.Storefront != "" {
		response.Header.Set("X-Set-Apple-Store-Front", options.Storefront)
	}
	if options.Pod != "" {
		response.Header.Set("Pod", options.Pod)
	}
	return Step{Name: "native authentication", Method: "POST", Url: "https://auth.itunes.apple.com/auth/v1/native", Header: header, Response: response,
		Check: func(request Request) error {
			var body map[string]any
			if _, err := plist.Unmarshal(request.Body, &body); err != nil || !reflect.DeepEqual(body, expected) {
				return fmt.Errorf("native authentication plist mismatch")
			}
			signature, err := base64.StdEncoding.DecodeString(request.Header.Get("X-Apple-ActionSignature"))
			if err != nil || len(signature) != 501 {
				return fmt.Errorf("invalid native action signature")
			}
			return nil
		},
	}, nil
}
