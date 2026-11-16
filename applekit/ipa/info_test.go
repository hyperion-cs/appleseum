package ipa_test

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hyperion-cs/appleseum/applekit/ipa"
	"howett.net/plist"
)

func TestInspectIpaMainBundle(t *testing.T) {
	for _, display := range []string{"", "Display &amp; Name"} {
		t.Run(display, func(t *testing.T) {
			path := writeIpa(t, []ipaEntry{
				{"Payload/Main.app/PlugIns/Nested.app/Info.plist", `<plist><dict><key>CFBundleIdentifier</key><string>test.example.nested</string><key>CFBundleVersion</key><string>99</string></dict></plist>`},
				{"Payload/Main.app/Info.plist", `<plist><dict><key>CFBundleIdentifier</key><string>test.example.main</string><key>CFBundleVersion</key><string>42</string><key>CFBundleName</key><string>Fallback</string><key>CFBundleDisplayName</key><string>` + display + `</string></dict></plist>`},
			})
			got, err := ipa.Inspect(path)
			if err != nil {
				t.Fatal(err)
			}
			name := "Fallback"
			if display != "" {
				name = "Display & Name"
			}
			if got != (ipa.Metadata{Bundle: "test.example.main", Version: "42", Name: name, DisplayVersion: "42"}) {
				t.Fatalf("wrong app metadata: %+v", got)
			}
		})
	}
}

func TestInspectIpaSeparatesDisplayVersionAndBuild(t *testing.T) {
	for _, format := range []int{plist.XMLFormat, plist.BinaryFormat} {
		for _, short := range []string{"1.4.1", ""} {
			info := map[string]string{"CFBundleIdentifier": "test.example.main", "CFBundleVersion": "2", "MinimumOSVersion": "17.5"}
			if short != "" {
				info["CFBundleShortVersionString"] = short
			}
			body, err := plist.Marshal(info, format)
			if err != nil {
				t.Fatal(err)
			}
			path := writeIpa(t, []ipaEntry{{"Payload/Main.app/Info.plist", string(body)}})
			got, err := ipa.Inspect(path)
			if err != nil {
				t.Fatal(err)
			}
			want := short
			if want == "" {
				want = "2"
			}
			if got.Version != "2" || got.DisplayVersion != want || got.MinimumOs != "17.5" {
				t.Fatalf("format=%d: build or display version lost: %+v", format, got)
			}
		}
	}
}

func TestInspectIpaRejectsInvalidMetadata(t *testing.T) {
	for name, entries := range map[string][]ipaEntry{
		"missing":      nil,
		"malformed":    {{"Payload/Main.app/Info.plist", "not a plist"}},
		"incomplete":   {{"Payload/Main.app/Info.plist", "<plist><dict/></plist>"}},
		"oversized":    {{"Payload/Main.app/Info.plist", strings.Repeat("x", 1_048_577)}},
		"empty bundle": {{"Payload/.app/Info.plist", `<plist><dict><key>CFBundleIdentifier</key><string>test.empty</string><key>CFBundleVersion</key><string>1</string></dict></plist>`}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ipa.Inspect(writeIpa(t, entries)); err == nil {
				t.Fatal("invalid metadata accepted")
			}
		})
	}
}

type ipaEntry struct{ name, body string }

func writeIpa(t *testing.T, entries []ipaEntry) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "synthetic.ipa")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	archive := zip.NewWriter(file)
	for _, entry := range entries {
		w, err := archive.Create(entry.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(entry.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}
