package adi

import (
	"sync"
	"testing"
)

func TestFixedFormatsConcurrent(t *testing.T) {
	// Alternating inputs catch accidentally shared CBC state as well as key races.
	var expected [2][6][32]byte
	var input [2][32]byte
	for i := range input[1] {
		input[1][i] = byte(i + 1)
	}

	transform := func(in [32]byte) [6][32]byte {
		var decoded [32]byte
		softwareStreamCipher.Decrypt(decoded[:], in[:])
		wrapped := encryptProvisioningStream(in)
		checksum := syncChecksum(in[:16])
		var amdField [272]byte
		var contextField [240]byte
		copy(amdField[:], in[:])
		copy(contextField[:], in[:])
		amdKey, contextKey := deriveAmdKey(amdField), deriveProvisioningContextKey(contextField)
		var material [400]byte
		copy(material[:], in[:])
		key := deriveSoftwareKey(material)
		var result [6][32]byte
		result[0], result[1] = decoded, wrapped
		copy(result[2][:], checksum[:])
		copy(result[3][:], amdKey[:])
		copy(result[4][:], contextKey[:])
		copy(result[5][:], key[:])
		return result
	}

	for i := range input {
		expected[i] = transform(input[i])
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 10 {
				for i := range input {
					if transform(input[i]) != expected[i] {
						t.Error("shared AES key or CBC IV changed across calls")
					}
				}
			}
		})
	}
	wg.Wait()
}

func TestProvisioningRandomIndependent(t *testing.T) {
	first, second := provisioningRandomStart, provisioningRandomStart
	var expected, got [128]byte
	first.FillNonzero(expected[:], provisioningRandomMask)
	first.FillNonzero(got[:], provisioningRandomMask) // Advancing one exchange must not advance another.
	second.FillNonzero(got[:], provisioningRandomMask)
	if got != expected {
		t.Fatal("exchanges share mutable random state")
	}
	third := provisioningRandomStart
	third.FillNonzero(got[:], provisioningRandomMask)
	if got != expected {
		t.Fatal("constructor template was modified")
	}
}
