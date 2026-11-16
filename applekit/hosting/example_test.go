package hosting_test

import (
	"fmt"
	"net/url"

	"github.com/hyperion-cs/appleseum/applekit/hosting"
	"github.com/hyperion-cs/appleseum/applekit/ipa"
)

func ExampleManifest() {
	// The application chooses where to host the IPA and manifest.
	metadata := ipa.Metadata{Bundle: "test.example.app", Version: "1", Name: "Example"}
	manifest, err := hosting.Manifest([]hosting.ManifestItem{{
		Metadata: metadata,
		Url:      "https://apps.example.test/files/example.ipa",
	}})
	if err != nil {
		panic(err)
	}
	// Serve manifest as XML at this address, and serve the IPA at the URL above.
	link := hosting.InstallUrl("https://apps.example.test/install/example.plist")
	parsed, err := url.Parse(link)
	if err != nil {
		panic(err)
	}
	fmt.Println(len(manifest) > 0)
	fmt.Println(parsed.Scheme, parsed.Query().Get("action"))
	fmt.Println(parsed.Query().Get("url"))
	// Output:
	// true
	// itms-services download-manifest
	// https://apps.example.test/install/example.plist
}
