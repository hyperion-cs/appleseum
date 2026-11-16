package hosting_test

import (
	"net/url"
	"strings"
	"testing"

	"github.com/hyperion-cs/appleseum/applekit/hosting"
	"github.com/hyperion-cs/appleseum/applekit/ipa"
	"howett.net/plist"
)

func TestInstallationManifestAndLink(t *testing.T) {
	items := []hosting.ManifestItem{
		{Metadata: ipa.Metadata{Bundle: "test.example.first", Version: "17", Name: "Приложение <one> & two"}, Url: "http://[::1]:8080/a%20b.ipa?ticket=one&other=two"},
		{Metadata: ipa.Metadata{Bundle: "test.example.second", Version: "2.3", Name: "Second"}, Url: "https://apps.example.test/second.ipa"},
	}
	body, err := hosting.Manifest(items)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Items []struct {
			Metadata struct {
				Bundle  string `plist:"bundle-identifier"`
				Version string `plist:"bundle-version"`
				Kind    string `plist:"kind"`
				Title   string `plist:"title"`
			} `plist:"metadata"`
			Assets []struct {
				Kind string `plist:"kind"`
				Url  string `plist:"url"`
			} `plist:"assets"`
		} `plist:"items"`
	}
	if _, err := plist.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Items) != len(items) {
		t.Fatalf("manifest lost apps: %+v", doc)
	}
	for i, want := range items {
		got := doc.Items[i]
		if got.Metadata.Bundle != want.Metadata.Bundle || got.Metadata.Version != want.Metadata.Version || got.Metadata.Title != want.Metadata.Name || got.Metadata.Kind != "software" || len(got.Assets) != 1 || got.Assets[0].Kind != "software-package" || got.Assets[0].Url != want.Url {
			t.Fatalf("manifest app %d: %+v", i, got)
		}
	}
	manifest := "https://[::1]:8443/a%20b.plist?ticket=one&label=Пример+two"
	link, err := url.Parse(hosting.InstallUrl(manifest))
	if err != nil {
		t.Fatal(err)
	}
	if link.Scheme != "itms-services" || link.Host != "" || link.Path != "" || link.Query().Get("action") != "download-manifest" || link.Query().Get("url") != manifest || len(link.Query()) != 2 {
		t.Fatalf("installation link lost or changed manifest URL: %s", link)
	}
}

func TestInstallationManifestRejectsIncompleteApps(t *testing.T) {
	if _, err := hosting.Manifest(nil); err == nil {
		t.Fatal("empty manifest accepted")
	}
	valid := hosting.ManifestItem{Metadata: ipa.Metadata{Bundle: "test.example.app", Version: "1"}, Url: "https://apps.example.test/a.ipa"}
	for _, raw := range []string{"", "/a.ipa", "file:///a.ipa", "https:///a.ipa", "https://user@apps.example.test/a.ipa", "https://apps.example.test/a.ipa#fragment"} {
		item := valid
		item.Url = raw
		if _, err := hosting.Manifest([]hosting.ManifestItem{item}); err == nil {
			t.Errorf("invalid IPA URL accepted: %q", raw)
		}
	}
	for _, metadata := range []ipa.Metadata{{Version: "1"}, {Bundle: "test.example.app"}} {
		item := valid
		item.Metadata = metadata
		if _, err := hosting.Manifest([]hosting.ManifestItem{item}); err == nil {
			t.Errorf("incomplete metadata accepted: %+v", metadata)
		}
	}
	if body, err := hosting.Manifest([]hosting.ManifestItem{valid}); err != nil || !strings.Contains(string(body), "<plist") {
		t.Fatal("untitled app must remain installable", err)
	}
}
