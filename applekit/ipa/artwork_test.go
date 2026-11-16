package ipa_test

import (
	"archive/zip"
	"image"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/hyperion-cs/appleseum/applekit/artwork"
	"github.com/hyperion-cs/appleseum/applekit/ipa"
)

func TestArtworkExtractionFromIpa(t *testing.T) {
	for _, name := range []string{"iTunesArtwork", "Payload/Test.app/AppIcon60x60@3x.png"} {
		path := filepath.Join(t.TempDir(), "test.ipa")
		file, _ := os.Create(path)
		archive := zip.NewWriter(file)
		info, _ := archive.Create("Payload/Test.app/Info.plist")
		io.WriteString(info, `<plist><dict><key>CFBundleIcons</key><dict><key>CFBundlePrimaryIcon</key><dict><key>CFBundleIconFiles</key><array><string>AppIcon60x60</string></array></dict></dict></dict></plist>`)
		icon, _ := archive.Create(name)
		png.Encode(icon, image.NewNRGBA(image.Rect(0, 0, 1, 1)))
		archive.Close()
		file.Close()
		if _, err := ipa.Artwork(path); err != nil {
			t.Fatal(err)
		}
	}
}

func TestArtworkFromIpaPreservesLargestMainAppIcon(t *testing.T) {
	path := filepath.Join(t.TempDir(), "synthetic.ipa")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	archive := zip.NewWriter(file)
	for _, entry := range []struct {
		name string
		size int
	}{
		{"Payload/Main.app/PlugIns/Nested.app/AppIcon.png", 512},
		{"Payload/Main.app/AppIcon@2x.png", 240},
		{"Payload/Main.app/AppIcon.png", 60},
	} {
		w, err := archive.Create(entry.name)
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(w, image.NewNRGBA(image.Rect(0, 0, entry.size, entry.size))); err != nil {
			t.Fatal(err)
		}
	}
	w, err := archive.Create("Payload/Main.app/Info.plist")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(w, "<plist><dict/></plist>"); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := ipa.Artwork(path)
	if err != nil {
		t.Fatal(err)
	}
	img, err := artwork.Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != 240 || img.Bounds().Dy() != 240 {
		t.Fatalf("main app icon lost resolution or came from a nested app: %v", img.Bounds())
	}
}
