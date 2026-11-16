package artwork

import (
	"bytes"
	"compress/flate"
	"context"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"testing"

	"github.com/hyperion-cs/appleseum/applekit/internal/applebackemu"
)

func pixel() []byte {
	var b bytes.Buffer
	img := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	img.SetNRGBA(0, 0, color.NRGBA{R: 200, G: 100, B: 50, A: 255})
	png.Encode(&b, img)
	return b.Bytes()
}

func TestFetchRestrictsAddressesAndRedirects(t *testing.T) {
	for _, raw := range []string{"http://is1.mzstatic.com/a", "https://127.0.0.1/a", "https://mzstatic.com.attacker.test/a", "https://is1.mzstatic.com:8080/a", "https://user@is1.mzstatic.com/a"} {
		backend := applebackemu.NewTest(t, applebackemu.InMemory)
		if _, err := fetch(context.Background(), raw, backend.Client().Transport); err == nil {
			t.Fatal("unsafe image address accepted")
		}
	}
	redirect := applebackemu.Artwork("https://is1.mzstatic.com/a", applebackemu.Response{StatusCode: 302, Header: http.Header{"Location": {"http://127.0.0.1/private"}}})
	backend := applebackemu.NewTest(t, applebackemu.InMemory, redirect)
	if _, err := fetch(context.Background(), "https://is1.mzstatic.com/a", backend.Client().Transport); err == nil || len(backend.Requests()) != 1 {
		t.Fatal("unsafe redirect followed")
	}
	step := applebackemu.Artwork("https://is1-ssl.mzstatic.com/a", applebackemu.Response{Body: pixel()})
	valid := applebackemu.NewTest(t, applebackemu.InMemory, step)
	data, err := fetch(context.Background(), "https://is1-ssl.mzstatic.com/a", valid.Client().Transport)
	if err != nil || len(data) == 0 {
		t.Fatal("valid image not fetched")
	}
}

func TestOptimizedIosPng(t *testing.T) {
	var raw bytes.Buffer
	writer, _ := flate.NewWriter(&raw, flate.DefaultCompression)
	writer.Write([]byte{0, 25, 50, 100, 128})
	writer.Close()
	var b bytes.Buffer
	b.WriteString("\x89PNG\r\n\x1a\n")

	chunk := func(kind string, data []byte) {
		binary.Write(&b, binary.BigEndian, uint32(len(data)))
		b.WriteString(kind)
		b.Write(data)
		crc := crc32.NewIEEE()
		crc.Write([]byte(kind))
		crc.Write(data)
		binary.Write(&b, binary.BigEndian, crc.Sum32())
	}

	header := make([]byte, 13)
	binary.BigEndian.PutUint32(header[:4], 1)
	binary.BigEndian.PutUint32(header[4:8], 1)
	header[8], header[9] = 8, 6
	chunk("CgBI", []byte{0, 0, 0, 0})
	chunk("IHDR", header)
	chunk("IDAT", raw.Bytes())
	chunk("IEND", nil)
	img, err := Decode(b.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	p := color.NRGBAModel.Convert(img.At(0, 0)).(color.NRGBA)
	if p.R != 199 || p.G != 99 || p.B != 49 || p.A != 128 {
		t.Fatalf("incorrect restored pixels: %+v", p)
	}
}

func TestDecodeArtworkBounds(t *testing.T) {
	for _, raw := range [][]byte{[]byte("<svg/>"), make([]byte, MaxBytes+1)} {
		if _, err := Decode(raw); err == nil {
			t.Fatal("invalid image accepted")
		}
	}
	var raw bytes.Buffer
	if err := png.Encode(&raw, image.NewNRGBA(image.Rect(0, 0, 1_025, 1))); err != nil {
		t.Fatal(err)
	}
	if _, err := Decode(raw.Bytes()); err == nil {
		t.Fatal("image dimensions were not bounded")
	}
}

func TestArtworkFetchBounds(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   []byte
	}{
		{"status", http.StatusServiceUnavailable, nil},
		{"size", http.StatusOK, make([]byte, MaxBytes+1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			step := applebackemu.Artwork("https://is1.mzstatic.com/synthetic", applebackemu.Response{StatusCode: tc.status, Body: tc.body})
			backend := applebackemu.NewTest(t, applebackemu.InMemory, step)
			_, err := fetch(context.Background(), "https://is1.mzstatic.com/synthetic", backend.Client().Transport)
			if err == nil {
				t.Fatal("failed or oversized response accepted")
			}
		})
	}
}

func fetch(ctx context.Context, raw string, transport http.RoundTripper) ([]byte, error) {
	client := NewClient(transport)
	defer client.CloseIdleConnections()
	return client.Fetch(ctx, raw)
}
