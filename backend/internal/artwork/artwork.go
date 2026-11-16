package artwork

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"

	appleartwork "github.com/hyperion-cs/appleseum/applekit/artwork"
)

const (
	MaxBytes = appleartwork.MaxBytes
)

func Normalize(data []byte) ([]byte, error) {
	if len(data) > MaxBytes {
		return nil, fmt.Errorf("artwork too large")
	}
	img, err := appleartwork.Decode(data)
	if err != nil {
		return nil, err
	}
	// Icons are displayed at 40 CSS pixels; 120 pixels covers three device pixels per CSS pixel.
	width, height := img.Bounds().Dx(), img.Bounds().Dy()
	if width > 120 || height > 120 {
		scale := float64(120) / float64(max(width, height))
		width = max(1, int(float64(width)*scale))
		height = max(1, int(float64(height)*scale))
		scaled := image.NewNRGBA(image.Rect(0, 0, width, height))
		bounds := img.Bounds()
		for y := 0; y < height; y++ {
			for x := 0; x < width; x++ {
				scaled.Set(x, y, img.At(bounds.Min.X+x*bounds.Dx()/width, bounds.Min.Y+y*bounds.Dy()/height))
			}
		}
		img = scaled
	}
	var out bytes.Buffer
	if err = png.Encode(&out, img); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func Save(dir string, data []byte) (string, error) {
	normalized, err := Normalize(data)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(normalized)
	name := hex.EncodeToString(sum[:]) + ".png"
	path := filepath.Join(dir, "icons", name)
	if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() && info.Size() == int64(len(normalized)) {
		return name, nil
	}
	file, err := os.CreateTemp(filepath.Join(dir, "icons"), ".icon-")
	if err != nil {
		return "", err
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(normalized); err != nil {
		file.Close()
		return "", err
	}
	if err = file.Close(); err != nil {
		return "", err
	}
	if err = os.Rename(file.Name(), path); err != nil {
		if info, e := os.Stat(path); e != nil || info.Size() != int64(len(normalized)) {
			return "", err
		}
	}
	return name, nil
}

func ValidName(name string) bool {
	if len(name) != 68 || !strings.HasSuffix(name, ".png") {
		return false
	}
	_, err := hex.DecodeString(name[:64])
	return err == nil && strings.ToLower(name) == name
}
