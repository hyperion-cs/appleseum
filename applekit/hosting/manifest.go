package hosting

import (
	"fmt"
	"net/url"

	"github.com/hyperion-cs/appleseum/applekit/ipa"
	"howett.net/plist"
)

// ManifestItem describes an IPA hosted by the calling application.
type ManifestItem struct {
	Metadata ipa.Metadata
	Url      string
}

// Manifest encodes an OTA installation manifest with explicit IPA URLs.
// Config.Manifest derives those URLs from server and connection facts.
func Manifest(items []ManifestItem) ([]byte, error) {
	if len(items) == 0 {
		return nil, fmt.Errorf("installation manifest has no apps")
	}
	entries := make([]map[string]any, 0, len(items))
	for _, item := range items {
		u, err := url.Parse(item.Url)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
			return nil, fmt.Errorf("invalid IPA URL")
		}
		if item.Metadata.Bundle == "" || item.Metadata.Version == "" {
			return nil, fmt.Errorf("missing bundle identifier or version")
		}
		entries = append(entries, map[string]any{
			"metadata": map[string]string{
				"bundle-identifier": item.Metadata.Bundle,
				"bundle-version":    item.Metadata.Version,
				"kind":              "software",
				"title":             item.Metadata.Name,
			},
			"assets": []map[string]string{{"kind": "software-package", "url": item.Url}},
		})
	}
	return plist.Marshal(map[string]any{"items": entries}, plist.XMLFormat)
}

// InstallUrl encodes an explicit HTTPS manifest URL as an install link.
// Config.InstallUrl selects the manifest address from connection facts.
func InstallUrl(manifestUrl string) string {
	return "itms-services://?action=download-manifest&url=" + url.QueryEscape(manifestUrl)
}
