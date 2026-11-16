package applekit

import (
	"slices"
	"strings"
	"testing"
)

func TestPlatformCatalogAndCompatibility(t *testing.T) {
	models, versions := DeviceModels(), MacOsVersions()
	seen := map[string]bool{}
	for i, model := range models {
		if seen[model.Id] || i > 0 && models[i-1].Year < model.Year || model.MinOs < 15 {
			t.Fatal("invalid model catalog", model)
		}
		seen[model.Id] = true
	}
	for _, id := range []string{"MacBookPro15,1", "MacBookPro16,4", "MacBookAir9,1", "Mac17,3", "Mac17,5", "Mac17,9"} {
		if !seen[id] {
			t.Fatal("supported notebook missing", id)
		}
	}
	for _, id := range []string{"MacBookAir8,2", "MacBookPro14,3", "MacBook10,1"} {
		if seen[id] {
			t.Fatal("unsupported notebook offered", id)
		}
	}
	for i := 1; i < len(versions); i++ {
		if slices.Compare(versionParts(versions[i-1].Version), versionParts(versions[i].Version)) <= 0 {
			t.Fatal("versions not newest first")
		}
	}
	for _, tc := range []struct {
		model, version string
		valid          bool
	}{
		{"MacBookAir9,1", "15.7", true}, {"MacBookAir9,1", "26.5", false},
		{"Mac17,3", "15.7", false}, {"Mac17,3", "26.5", true},
		{"MacBookPro16,4", "27.2", false}, {"MacBookPro18,1", "27.2", true},
		{"MacBookPro18,1\r\nInjected: true", "26.5", false},
		{"MacBookPro18,1", "99.9", false},
	} {
		d := DefaultDeviceSettings()
		d.Model, d.OsVersion = tc.model, tc.version
		if (d.validate() == nil) != tc.valid {
			t.Fatal("wrong compatibility decision", tc)
		}
	}
	models[0].Name = "changed"
	versions[0].Build = "changed"
	if strings.Contains(DeviceModels()[0].Name, "changed") || MacOsVersions()[0].Build == "changed" {
		t.Fatal("caller mutated shared catalog")
	}
}
