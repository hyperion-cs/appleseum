package adi

import (
	"encoding/binary"
	"math/bits"

	"github.com/hyperion-cs/appleseum/applekit/internal/cryptoutil"
)

// IdMS/AMD digests use SHA-256 padding with custom schedules, rounds and feed-forwards.
type digestParameters struct {
	Initial [8]uint32
	Rounds  [64]uint32
}

var idmsFormat = cryptoutil.ReadAsset[digestParameters](formatAssets, "assets/idms-digest.bin")
var amdFormat = cryptoutil.ReadAsset[digestParameters](formatAssets, "assets/amd-digest.bin")

func idmsDigest(input []byte) [32]byte { return idmsFormat.sum(input, compressIdms) }

func amdDigest(input []byte) [32]byte { return amdFormat.sum(input, compressAmd) }

// Use SHA-256 padding with at most two tail blocks.
func (p *digestParameters) sum(input []byte, compress func(*digestParameters, *[8]uint32, *[64]byte)) (digest [32]byte) {
	bitLength := uint64(len(input)) * 8
	state := p.Initial
	for len(input) >= 64 {
		compress(p, &state, (*[64]byte)(input))
		input = input[64:]
	}
	var tail [128]byte
	tail[copy(tail[:], input)] = 0x80
	size := 64
	if len(input) >= 56 {
		size = 128
	}
	binary.BigEndian.PutUint64(tail[size-8:size], bitLength)
	for i := 0; i < size; i += 64 {
		compress(p, &state, (*[64]byte)(tail[i:]))
	}
	for i, word := range state {
		binary.BigEndian.PutUint32(digest[4*i:], word)
	}
	return
}

func compressIdms(p *digestParameters, state *[8]uint32, block *[64]byte) {
	var schedule [64]uint32
	for i := range schedule {
		if i < 16 {
			schedule[i] = binary.BigEndian.Uint32(block[4*i:])
			continue
		}
		x, y := schedule[i-2], schedule[i-15]
		sigma0 := (bits.RotateLeft32(y, -7) ^ bits.RotateLeft32(y, -18)) - (y >> 3)
		sigma1 := bits.RotateLeft32(x, -17) ^ bits.RotateLeft32(x, -19) ^ (x >> 10)
		schedule[i] = (sigma0 ^ sigma1 ^ schedule[i-7]) + schedule[i-16]
	}

	a, b, c, d := state[0], state[1], state[2], state[3]
	e, f, g, h := state[4], state[5], state[6], state[7]
	for i, word := range schedule {
		sigmaE := bits.RotateLeft32(e, -6) - bits.RotateLeft32(e, -11) + bits.RotateLeft32(e, -25)
		mixEfg := 2*g - (e + (e ^ f))
		temporary := (sigmaE ^ h) + mixEfg + p.Rounds[i] - word
		sigmaA := bits.RotateLeft32(a, -2) ^ bits.RotateLeft32(a, -13) ^ bits.RotateLeft32(a, -22)
		mixAbc := 2 * ((a & c) - (a & b))
		a, b, c, d, e, f, g, h = (sigmaA^mixAbc)+temporary, a, b, c, d+temporary, e, f, g
	}
	*state = [8]uint32{
		a - state[0], b ^ state[1], c - state[2], d ^ state[3],
		e + state[4], f ^ state[5], g - state[6], h - state[7],
	}
}

// AMD only hashes one counter block, so the incoming state is always the initial vector.
func compressAmd(p *digestParameters, state *[8]uint32, block *[64]byte) {
	var schedule [64]uint32
	for i := range schedule {
		if i < 16 {
			schedule[i] = binary.BigEndian.Uint32(block[4*i:])
			continue
		}
		x, y := schedule[i-2], schedule[i-15]
		sigma0 := bits.RotateLeft32(y, -7) + bits.RotateLeft32(y, -18) - (y >> 3)
		sigma1 := bits.RotateLeft32(x, -17) - bits.RotateLeft32(x, -19) - (x >> 10)
		schedule[i] = (sigma0 ^ sigma1) + schedule[i-7] - schedule[i-16]
	}

	a, b, c, d := state[0], state[1], state[2], state[3]
	e, f, g, h := state[4], state[5], state[6], state[7]
	for i, word := range schedule {
		sigmaE := (bits.RotateLeft32(e, -6) ^ bits.RotateLeft32(e, -11)) + bits.RotateLeft32(e, -25)
		mixEfg := e + (e ^ f) + 2*g
		temporary := sigmaE + h + mixEfg + p.Rounds[i] + word
		sigmaA := bits.RotateLeft32(a, -2) - bits.RotateLeft32(a, -13) - bits.RotateLeft32(a, -22)
		mixAbc := 2 * (b - (a &^ c))
		a, b, c, d, e, f, g, h = sigmaA+mixAbc+temporary, a, b, c, d+temporary, e, f, g
	}
	*state = [8]uint32{
		a - state[0], b - state[1], c - state[2], d + state[3],
		e - state[4], f + state[5], g ^ state[6], h ^ state[7],
	}
}
