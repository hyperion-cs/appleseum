package storekit

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"context"
	"errors"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"howett.net/plist"
)

func rawSource(t *testing.T, badCrc bool, trailing ...byte) (string, map[string][]byte) {
	t.Helper()
	contents := map[string][]byte{
		"Payload/Test.app/Info.plist": []byte(`<?xml version="1.0"?><plist version="1.0"><dict><key>CFBundleExecutable</key><string>Test</string></dict></plist>`),
		"Payload/Test.app/Test":       bytes.Repeat([]byte("licensed app payload\x00"), 10_000),
	}
	var source bytes.Buffer
	writer := zip.NewWriter(&source)
	for _, name := range []string{"Payload/Test.app/Info.plist", "Payload/Test.app/Test"} {
		data := contents[name]
		var compressed bytes.Buffer
		compressor, err := flate.NewWriter(&compressed, flate.BestCompression)
		if err != nil {
			t.Fatal(err)
		}
		compressor.Write(data)
		compressor.Close()
		compressed.Write(trailing)
		crc := crc32.ChecksumIEEE(data)
		if badCrc && name == "Payload/Test.app/Test" {
			crc++
		}
		header := &zip.FileHeader{Name: name, Method: zip.Deflate, Flags: 0x800, CreatorVersion: 3<<8 | 30, ReaderVersion: 20, CRC32: crc, CompressedSize64: uint64(compressed.Len()), UncompressedSize64: uint64(len(data)), ExternalAttrs: 0o100755 << 16}
		entry, err := writer.CreateRaw(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = entry.Write(compressed.Bytes()); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "source.ipa")
	if err := os.WriteFile(path, source.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	return path, contents
}

func TestAssemblyPreservesPayloadAndOmitsAccountData(t *testing.T) {
	src, contents := rawSource(t, false)
	dst := filepath.Join(t.TempDir(), "out.ipa")
	item := map[string]any{
		"sinfs": []any{map[string]any{"sinf": []byte("license")}},
		"metadata": map[string]any{
			"itemName": "Example", "softwareVersionBundleId": "test.example",
			"softwareVersionExternalIdentifier": uint64(7),
			"appleId":                           "old@example.test", "apple-id": "other@example.test", "AppleID": "+15555550123",
			"userName": "Example Person", "UserName": "Other Person",
			"purchaseDate": "2026-01-01T00:00:00Z", "storeCohort": "account cohort",
			"com.apple.iTunesStore.downloadInfo": map[string]any{
				"accountInfo": map[string]any{
					"AppleID": "old@example.test", "UserName": "Example Person",
					"DSPersonID": uint64(456), "PurchaserID": uint64(456),
					"FirstName": "Example", "LastName": "Person",
				},
			},
		},
	}
	if err := assembleIpa(context.Background(), src, dst, item); err != nil {
		t.Fatal(err)
	}
	after, err := zip.OpenReader(dst)
	if err != nil {
		t.Fatal(err)
	}
	defer after.Close()
	found := map[string][]byte{}
	for _, file := range after.File {
		reader, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(reader)
		reader.Close()
		if err != nil {
			t.Fatal(err)
		}
		found[file.Name] = data
		if expected, ok := contents[file.Name]; ok {
			if !bytes.Equal(data, expected) {
				t.Fatal("application bytes changed")
			}
			if file.CreatorVersion&0xff != 20 || file.Flags&8 == 0 || file.Method != zip.Deflate || file.ExternalAttrs != 0o100755<<16 {
				t.Fatal("ZIP headers no longer match the known-installable layout")
			}
		}
	}
	if !bytes.Equal(found["Payload/Test.app/SC_Info/Test.sinf"], []byte("license")) {
		t.Fatal("account license missing or changed")
	}
	var meta map[string]any
	if _, err = plist.Unmarshal(found["iTunesMetadata.plist"], &meta); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"itemName": "Example", "softwareVersionBundleId": "test.example",
		"softwareVersionExternalIdentifier": uint64(7), "is-purchased-redownload": true,
	}
	if !reflect.DeepEqual(meta, want) {
		t.Fatalf("unexpected IPA metadata: %#v", meta)
	}
}

func TestAssemblyRejectsPayloadCrcFailure(t *testing.T) {
	src, _ := rawSource(t, true)
	if err := assembleIpa(context.Background(), src, filepath.Join(t.TempDir(), "out.ipa"), map[string]any{"sinfs": []any{map[string]any{"sinf": []byte("license")}}}); err == nil {
		t.Fatal("corrupt payload was accepted without a CRC check")
	}
}

// Exercise ZIP entry reuse on every platform, including deferred closes.
func TestReuseZipEntry(t *testing.T) {
	for _, mode := range []string{"valid", "bad CRC", "canceled", "trailing bytes"} {
		t.Run(mode, func(t *testing.T) {
			var trailing []byte
			if mode == "trailing bytes" {
				trailing = bytes.Repeat([]byte{0xa7}, 65_536)
			}
			src, _ := rawSource(t, mode == "bad CRC", trailing...)
			data, err := os.ReadFile(src)
			if err != nil {
				t.Fatal(err)
			}
			reads := &zipBodyReads{ReaderAt: bytes.NewReader(data)}
			input, err := zip.NewReader(reads, int64(len(data)))
			if err != nil {
				t.Fatal(err)
			}
			var expected int64
			for _, f := range input.File {
				offset, err := f.DataOffset()
				if err != nil {
					t.Fatal(err)
				}
				reads.bodies = append(reads.bodies, [2]int64{offset, offset + int64(f.CompressedSize64)})
				expected += int64(f.CompressedSize64)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var encoded bytes.Buffer
			writer := zip.NewWriter(&encoded)
			buffer := make([]byte, 32_768)
			if mode == "canceled" {
				cancel()
			}
			err = reuseZipEntries(ctx, input, writer, nil, buffer)
			if mode == "bad CRC" {
				if !errors.Is(err, zip.ErrChecksum) {
					t.Fatalf("CRC: %v", err)
				}
				return
			}
			if mode == "canceled" {
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("cancel: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if reads.bytes != expected {
				t.Fatalf("compressed reads=%d, expected one pass=%d", reads.bytes, expected)
			}
			// Both registries must be back to standard codecs after copying.
			originalReader, err := input.File[0].Open()
			if err != nil {
				t.Fatal(err)
			}
			_, err = io.Copy(io.Discard, originalReader)
			originalReader.Close()
			if err != nil {
				t.Fatal(err)
			}
			metadata, err := writer.Create("added metadata")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := metadata.Write([]byte("new metadata")); err != nil {
				t.Fatal(err)
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			output, err := zip.NewReader(bytes.NewReader(encoded.Bytes()), int64(encoded.Len()))
			if err != nil {
				t.Fatal(err)
			}
			if len(output.File) != len(input.File)+1 {
				t.Fatal("entry count changed")
			}
			for i, file := range output.File[:len(input.File)] {
				if file.Name != input.File[i].Name || file.CreatorVersion&0xff != 20 || file.Flags&8 == 0 || file.Method != zip.Deflate || file.ExternalAttrs != input.File[i].ExternalAttrs {
					t.Fatal("ZIP header normalization lost")
				}
				original, err := input.File[i].OpenRaw()
				if err != nil {
					t.Fatal(err)
				}
				want, err := io.ReadAll(original)
				if err != nil {
					t.Fatal(err)
				}
				raw, err := file.OpenRaw()
				if err != nil {
					t.Fatal(err)
				}
				got, err := io.ReadAll(raw)
				if err != nil || !bytes.Equal(got, want) {
					t.Fatalf("Deflate stream changed: %v", err)
				}
				reader, err := file.Open()
				if err != nil {
					t.Fatal(err)
				}
				_, err = io.Copy(io.Discard, reader)
				reader.Close()
				if err != nil {
					t.Fatal("output CRC", err)
				}
			}
			reader, err := output.File[len(input.File)].Open()
			if err != nil {
				t.Fatal(err)
			}
			added, err := io.ReadAll(reader)
			reader.Close()
			if err != nil || string(added) != "new metadata" {
				t.Fatalf("added metadata: %q %v", added, err)
			}

		})
	}
}

// Count body reads separately from ZIP directory/header parsing.
type zipBodyReads struct {
	io.ReaderAt
	bodies [][2]int64
	bytes  int64
}

func (r *zipBodyReads) ReadAt(p []byte, offset int64) (int, error) {
	n, err := r.ReaderAt.ReadAt(p, offset)
	for _, body := range r.bodies {
		r.bytes += max(0, min(offset+int64(n), body[1])-max(offset, body[0]))
	}
	return n, err
}
