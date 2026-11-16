package artwork

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func pixel() []byte {
	var b bytes.Buffer
	img := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	img.SetNRGBA(0, 0, color.NRGBA{R: 200, G: 100, B: 50, A: 255})
	png.Encode(&b, img)
	return b.Bytes()
}

func TestCacheNormalizesAndDeduplicates(t *testing.T) {
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, "icons"), 0700)
	a, err := Save(dir, pixel())
	if err != nil || !ValidName(a) {
		t.Fatal(err)
	}
	b, err := Save(dir, pixel())
	if err != nil || a != b {
		t.Fatal("image not deduplicated")
	}
	if _, err = Save(dir, []byte("<svg/>")); err == nil {
		t.Fatal("SVG accepted")
	}
	if _, err = Normalize(make([]byte, MaxBytes+1)); err == nil {
		t.Fatal("oversized image accepted")
	}
	files, _ := os.ReadDir(filepath.Join(dir, "icons"))
	if len(files) != 1 {
		t.Fatal("temporary icon left behind")
	}
}

func TestNormalizeScalesForDisplay(t *testing.T) {
	var raw bytes.Buffer
	img := image.NewNRGBA(image.Rect(0, 0, 240, 120))
	img.SetNRGBA(120, 60, color.NRGBA{R: 200, G: 100, B: 50, A: 255})
	if err := png.Encode(&raw, img); err != nil {
		t.Fatal(err)
	}
	data, err := Normalize(raw.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	scaled, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if scaled.Bounds().Dx() != 120 || scaled.Bounds().Dy() != 60 {
		t.Fatal("display bounds changed", scaled.Bounds())
	}
	if got := color.NRGBAModel.Convert(scaled.At(60, 30)).(color.NRGBA); got != (color.NRGBA{R: 200, G: 100, B: 50, A: 255}) {
		t.Fatal("display scaling lost source pixels", got)
	}
}
