package cryptoutil

import (
	"testing"
	"testing/fstest"
)

func TestReadAssetPackedLittleEndian(t *testing.T) {
	type parameters struct {
		Prefix byte
		Words  [2]uint32
	}
	files := fstest.MapFS{"parameters.bin": {Data: []byte{9, 4, 3, 2, 1, 8, 7, 6, 5}}}
	got := ReadAsset[parameters](files, "parameters.bin")
	if got.Prefix != 9 || got.Words != [2]uint32{0x01020304, 0x05060708} {
		t.Fatalf("incorrect packed byte order: %+v", got)
	}
	got.Words[0] = 0
	if next := ReadAsset[parameters](files, "parameters.bin"); next.Words[0] != 0x01020304 {
		t.Fatal("working copy changed the embedded parameters")
	}
}

func TestReadAssetRejectsMissingAndWrongSize(t *testing.T) {
	files := fstest.MapFS{
		"short.bin": {Data: []byte{1, 2, 3}},
		"long.bin":  {Data: []byte{1, 2, 3, 4, 5}},
	}
	for _, name := range []string{"missing.bin", "short.bin", "long.bin"} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("invalid embedded asset accepted")
				}
			}()
			ReadAsset[[2]uint16](files, name)
		})
	}
}
