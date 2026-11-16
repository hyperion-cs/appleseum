package cryptoutil

import (
	"math/bits"
	"testing"
)

func TestLinearByteMaps(t *testing.T) {
	for _, tc := range []struct {
		name  string
		basis [8]byte
		want  func(byte) byte
	}{
		{"identity", [8]byte{1, 2, 4, 8, 16, 32, 64, 128}, func(v byte) byte { return v }},
		{"reverse", [8]byte{128, 64, 32, 16, 8, 4, 2, 1}, bits.Reverse8},
		{"adjacent parity", [8]byte{3, 6, 12, 24, 48, 96, 192, 129}, func(v byte) byte { return v ^ bits.RotateLeft8(v, 1) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			table := LinearByteTable(tc.basis)
			for i := range table {
				want := tc.want(byte(i))
				if table[i] != want || ApplyByteMap(byte(i), tc.basis) != want {
					t.Fatalf("byte %d", i)
				}
			}
		})
	}
}
