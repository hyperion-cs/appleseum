package applebackemu

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"howett.net/plist"
)

const (
	assetAgent = "Configurator/2.18 (Macintosh; OS X 15.3.2; 24D81) AppleWebKit/0620.2.4.11.6"
)

// DownloadOptions specifies the expected dispatch identity and headers.
// An empty Keybag accepts any nonempty kbsync.
type DownloadOptions struct {
	Endpoint                      string
	Guid, Dsid, Storefront, Token string
	AppId                         int64
	Version, Keybag, Serial       string
	Authorized                    bool
	Header                        http.Header
}

// DownloadProduct checks a dispatch request against a caller-supplied response.
func DownloadProduct(options DownloadOptions, response Response) (Step, error) {
	if options.Endpoint == "" {
		options.Endpoint = "https://downloaddispatch.itunes.apple.com/WebObjects/DownloadDispatch.woa/wa/ent/download"
	}
	u, err := url.Parse(options.Endpoint)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" {
		return Step{}, fmt.Errorf("applebackemu: invalid dispatch endpoint")
	}
	query := u.Query()
	query.Set("guid", options.Guid)
	u.RawQuery = query.Encode()
	header := http.Header{}
	for name, value := range map[string]string{
		"User-Agent": assetAgent, "Content-Type": "application/x-www-form-urlencoded; charset=utf-8",
		"iCloud-DSID": options.Dsid, "X-Dsid": options.Dsid,
		"X-Apple-Store-Front": options.Storefront, "X-Token": options.Token,
	} {
		header.Set(name, value)
	}
	for name, values := range options.Header {
		header[http.CanonicalHeaderKey(name)] = append([]string(nil), values...)
	}
	expected := map[string]string{"creditDisplay": "", "guid": options.Guid, "salableAdamId": strconv.FormatInt(options.AppId, 10)}
	if options.Version != "" {
		expected["externalVersionId"] = options.Version
	}
	if options.Authorized {
		expected["hasBeenAuthedForBuy"] = "true"
	}
	return Step{Name: "download product", Method: "POST", Url: u.String(), Header: header, Response: response,
		Check: func(request Request) error {
			var body map[string]any
			if _, err := plist.Unmarshal(request.Body, &body); err != nil {
				return fmt.Errorf("invalid dispatch plist")
			}
			if len(body) != len(expected)+2 {
				return fmt.Errorf("dispatch field count mismatch")
			}
			for name, value := range expected {
				if body[name] != value {
					return fmt.Errorf("dispatch field %s mismatch", name)
				}
			}
			kb, _ := body["kbsync"].(string)
			serial, _ := body["serialNumber"].(string)
			if kb == "" || (options.Keybag != "" && kb != options.Keybag) {
				return fmt.Errorf("dispatch kbsync mismatch")
			}
			decoded, err := base64.StdEncoding.DecodeString(serial)
			if err != nil || len(decoded) < 5 || (options.Serial != "" && serial != options.Serial) {
				return fmt.Errorf("dispatch serial mismatch")
			}
			return nil
		},
	}, nil
}

// DownloadAsset checks an asset GET or redirect.
// OpenBody can simulate read failures; ContentLength may be unknown.
func DownloadAsset(endpoint string, response Response) Step {
	return Step{Name: "download asset", Method: "GET", Url: endpoint,
		Header: http.Header{"User-Agent": {assetAgent}}, Body: []byte{}, Response: response,
	}
}
