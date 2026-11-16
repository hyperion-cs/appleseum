package applebackemu

import "net/http"

// Artwork describes one public CDN fetch or redirect.
func Artwork(endpoint string, response Response) Step {
	return Step{Name: "artwork CDN", Method: "GET", Url: endpoint, Body: []byte{}, Response: response,
		Header: http.Header{"User-Agent": {"Configurator/2.21 Configurator/2.21 (Macintosh; OS X 26.5; 25F71) AppleWebKit/1624.2.5.11.4 AMS/1 (dt:1)"}},
	}
}
