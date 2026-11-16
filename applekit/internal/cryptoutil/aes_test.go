package cryptoutil

import (
	"bytes"
	"encoding/hex"
	"testing"
)

func TestReverseAes128KeyFips197(t *testing.T) {
	// AES-128 cipher example from FIPS 197 (rounds zero through ten).
	rounds := []string{
		"000102030405060708090a0b0c0d0e0f",
		"d6aa74fdd2af72fadaa678f1d6ab76fe",
		"b692cf0b643dbdf1be9bc5006830b3fe",
		"b6ff744ed2c2c9bf6c590cbf0469bf41",
		"47f7f7bc95353e03f96c32bcfd058dfd",
		"3caaa3e8a99f9deb50f3af57adf622aa",
		"5e390f7df7a69296a7553dc10aa31f6b",
		"14f9701ae35fe28c440adf4d4ea9c026",
		"47438735a41c65b9e016baf4aebf7ad2",
		"549932d1f08557681093ed9cbe2c974e",
		"13111d7fe3944a17f307a78b4d2b30c5",
	}
	for round, encoded := range rounds {
		key, err := hex.DecodeString(encoded)
		if err != nil {
			t.Fatal(err)
		}
		original := ReverseAes128Key([16]byte(key), round)
		if hex.EncodeToString(original[:]) != rounds[0] {
			t.Fatalf("round %d recovered %x", round, original)
		}
	}
}

func TestReadPaddingRejectsSourceZerosBeforeEncoding(t *testing.T) {
	for _, tc := range []struct {
		mask byte
		want []byte
	}{
		{0, []byte{7, 45, 1, 9}},
		{45, []byte{42, 0, 44, 36}},
	} {
		entropy := bytes.NewReader([]byte{0, 45, 1, 0, 7, 9})
		out := make([]byte, 4)
		if err := ReadPadding(entropy, out, tc.mask); err != nil || !bytes.Equal(out, tc.want) || entropy.Len() != 0 {
			t.Fatalf("padding encoding differs for mask %d: %v", tc.mask, err)
		}
	}
	for _, input := range [][]byte{nil, {1}, {0, 0}} {
		if err := ReadPadding(bytes.NewReader(input), make([]byte, 2), 45); err == nil {
			t.Fatal("incomplete padding entropy accepted")
		}
	}
}
