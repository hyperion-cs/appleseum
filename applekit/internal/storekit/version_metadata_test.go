package storekit

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hyperion-cs/appleseum/applekit/internal/applebackemu"
)

func versionIpa(t *testing.T, zip64 bool, comment string) []byte {
	t.Helper()
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	z.SetComment(comment)
	// Keep the plist beyond the prefix and away from the tail to exercise fallback.
	for _, entry := range []struct{ name, body string }{
		{"Payload/Test.app/before", strings.Repeat("x", 2_097_152)},
		{"Payload/Test.app/PlugIns/Nested.app/Info.plist", `<plist><dict><key>CFBundleShortVersionString</key><string>99</string></dict></plist>`},
		{"Payload/Test.app/Info.plist", `<plist><dict><key>CFBundleShortVersionString</key><string>2.4.1</string><key>CFBundleVersion</key><string>81</string><key>MinimumOSVersion</key><string>16.2</string></dict></plist>`},
		{"Payload/Test.app/data", strings.Repeat("x", 2_097_152)},
	} {
		w, err := z.CreateHeader(&zip.FileHeader{Name: entry.name, Method: zip.Store})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = w.Write([]byte(entry.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	raw := b.Bytes()
	if !zip64 {
		return raw
	}
	// Promote the ordinary EOCD to ZIP64 without needing a multi-gigabyte fixture.
	offset := len(raw) - 22 - len(comment)
	end := append([]byte(nil), raw[offset:]...)
	count := binary.LittleEndian.Uint16(end[10:])
	size := binary.LittleEndian.Uint32(end[12:])
	start := binary.LittleEndian.Uint32(end[16:])
	var ext bytes.Buffer
	for _, v := range []any{uint32(0x06064b50), uint64(44), uint16(45), uint16(45), uint32(0), uint32(0), uint64(count), uint64(count), uint64(size), uint64(start), uint32(0x07064b50), uint32(0), uint64(offset), uint32(1)} {
		binary.Write(&ext, binary.LittleEndian, v)
	}
	binary.LittleEndian.PutUint16(end[8:], 0xffff)
	binary.LittleEndian.PutUint16(end[10:], 0xffff)
	binary.LittleEndian.PutUint32(end[12:], 0xffffffff)
	binary.LittleEndian.PutUint32(end[16:], 0xffffffff)
	return append(append(append([]byte{}, raw[:offset]...), ext.Bytes()...), end...)
}

func TestVersionInfoReadsOnlyRanges(t *testing.T) {
	for _, tc := range []struct {
		name    string
		zip64   bool
		comment string
		mode    applebackemu.Mode
	}{{"plain", false, "", applebackemu.InMemory}, {"comment", false, strings.Repeat("c", 65_535), applebackemu.InMemory}, {"zip64", true, "", applebackemu.InMemory}, {"https", false, "", applebackemu.HTTPS}} {
		t.Run(tc.name, func(t *testing.T) {
			raw := versionIpa(t, tc.zip64, tc.comment)
			steps, err := applebackemu.RangeAsset(fixtureAssetUrl, raw, 3)
			if err != nil {
				t.Fatal(err)
			}
			steps[0].Header.Set("Range", "bytes=-66560")
			var transferred atomic.Int64
			for i := range steps {
				respond := steps[i].Respond

				steps[i].Respond = func(request applebackemu.Request) (applebackemu.Response, error) {
					response, err := respond(request)
					transferred.Add(int64(len(response.Body)))
					return response, err
				}
			}
			session, backend := downloadBackend(t, tc.mode, downloadAccount(), fixtureDispatchUrl, steps...)
			got, err := readRemoteVersion(context.Background(), session.Client, fixtureAssetUrl, nil)
			if err != nil {
				t.Fatal(err)
			}
			if got != (VersionMetadata{Version: "2.4.1", Build: "81", MinimumOs: "16.2"}) {
				t.Fatalf("metadata %+v", got)
			}
			if transferred.Load() > 210_000 || len(backend.Requests()) > 3 {
				t.Fatalf("excessive fetch %d bytes, %d calls", transferred.Load(), len(backend.Requests()))
			}
		})
	}
}

func TestVersionInfoRejectsUnboundedAndWrongRanges(t *testing.T) {
	for _, tc := range []struct {
		name         string
		status       int
		header, body string
	}{{"full response", 200, "", strings.Repeat("x", 1_048_576)}, {"wrong offset", 206, "bytes 0-1/100000", "xx"}, {"inconsistent length", 206, "bytes 0-1/2", "x"}, {"trailing header", 206, "bytes 0-1/2 garbage", "xx"}} {
		t.Run(tc.name, func(t *testing.T) {
			response := applebackemu.Response{StatusCode: tc.status, Header: http.Header{"Content-Range": {tc.header}}, Body: []byte(tc.body)}
			step := applebackemu.DownloadAsset(fixtureAssetUrl, response)
			step.Header.Set("Range", "bytes=-66560")
			step.Header.Set("Accept-Encoding", "identity")
			session, _ := downloadBackend(t, applebackemu.InMemory, downloadAccount(), fixtureDispatchUrl, step)
			if _, err := readRemoteVersion(context.Background(), session.Client, fixtureAssetUrl, nil); err == nil {
				t.Fatal("bad response accepted")
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := readRemoteVersion(ctx, http.DefaultClient, "https://example.test/asset", nil); err == nil {
		t.Fatal("cancellation ignored")
	}
}
