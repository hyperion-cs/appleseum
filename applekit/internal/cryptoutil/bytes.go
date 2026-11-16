package cryptoutil

import (
	"crypto/subtle"
	"math/bits"
)

// Repeat fills dst with repetitions of a nonempty pattern.
func Repeat(dst, pattern []byte) {
	for filled := copy(dst, pattern); filled < len(dst); {
		filled += copy(dst[filled:], dst[:filled])
	}
}

// ApplyByteMap applies a linear XOR mapping given by the images of eight bits.
func ApplyByteMap(value byte, basis [8]byte) (result byte) {
	for bit, image := range basis {
		if value&(1<<bit) != 0 {
			result ^= image
		}
	}
	return
}

// LinearByteTable expands eight basis images to all 256 byte values.
func LinearByteTable(basis [8]byte) (table [256]byte) {
	size := 1
	for _, image := range basis {
		for value := range size {
			table[size+value] = table[value] ^ image
		}
		size *= 2
	}
	return
}

// Select chooses each bit from yes where mask is set, otherwise from no.
func Select(mask, yes, no byte) byte { return mask&yes | ^mask&no }

// Majority sets each bit that is set in at least two inputs.
func Majority(a, b, c byte) byte { return a&b | c&(a|b) }

// Counts wrap modulo 8; unlike bits.RotateLeft8, a zero count returns zero.
func Rotate(value, count byte) byte {
	if count &= 7; count == 0 {
		return 0
	}
	return bits.RotateLeft8(value, int(count))
}

// Index subtraction wraps as uint32 before modulo.
// Reads observe preceding writes, including across passes.
func MixState(state []byte) {
	size := uint32(len(state))
	for pass := uint32(0); pass < 4; pass++ {
		for i := range state {
			step := pass*size + uint32(i)
			state[i] = (state[i] ^ bits.RotateLeft8(state[(step-13)%size], 3)) +
				bits.RotateLeft8(state[(step-57)%size], 5) - bits.RotateLeft8(state[(step-155)%size], 7)
		}
	}
}

// MixOutput applies 256 in-place steps; start must be a multiple of len(state).
// Index subtraction wraps as uint32 before modulo.
func MixOutput(state []byte, start uint32) {
	size := uint32(len(state))
	for i := start; i < start+256; i++ {
		at := func(lag uint32) byte { return state[(i-lag)%size] }

		state[i%size] ^= bits.RotateLeft8(at(7), 1) ^ bits.RotateLeft8(at(37), 6) ^ bits.RotateLeft8(at(177), 5)
	}
}

// XorFold folds each source cyclically from dst[0]; dst must be nonempty.
func XorFold(dst []byte, sources ...[]byte) {
	for _, src := range sources {
		for len(src) > 0 {
			n := subtle.XORBytes(dst, dst, src)
			src = src[n:]
		}
	}
}
