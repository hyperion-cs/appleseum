package sapkey

import "github.com/hyperion-cs/appleseum/applekit/internal/cryptoutil"

// MixColumns(incoming bias) XOR outgoing bias is the same byte in every lane.
const (
	sessionRoundBias byte = 0x47
)

func sessionKey(state []byte) [16]byte {
	shareOffsets := [...]int{0x11c, 0x120, 0x94, 0x238}
	var key [16]byte
	for column := 0; column < 4; column++ {
		for b := 0; b < 4; b++ {
			i := column*4 + b
			value := state[0xd0+((7+column)%4)*4+b] ^ state[shareOffsets[column]+b]
			key[i] = tagByteDecode[value]
		}
	}
	key = mixColumns(key)
	for i, value := range roundEightParameters(state) {
		key[i] ^= tagByteDecode[value] ^ sessionRoundBias
	}
	// The session format supplies round eight of an AES-128 key schedule.
	return cryptoutil.ReverseAes128Key(key, 8)
}

func roundEightParameters(state []byte) [16]byte {
	var control [4]byte
	for i := range control {
		control[i] = state[0xc0+i] ^ state[0x30+i]
	}
	var counts [16]int
	moduli := [...]byte{5, 25, 45, 75}
	countByFlag := [...]int{3, 3, 5, 1}
	total := 0
	for i := range counts {
		flag := control[i/4] % moduli[i%len(moduli)] % 4
		counts[i] = countByFlag[flag]
		total += counts[i]
	}
	for position := int(control[1]) % len(counts); total > 32; position = (position + 5) % len(counts) {
		total -= counts[position] - 1
		counts[position] = 1
	}
	var parameters [16]byte
	consumed := 0
	for i, count := range counts {
		value := byte(0)
		for j := 0; j < count; j++ {
			value ^= state[0x170+consumed+j] ^ sessionFormat.ColumnMask[(consumed+j)%16]
		}
		parameters[i] = value
		consumed += count
	}
	return parameters
}

// The XOR-linear byte encoding is represented by eight basis images.
var tagByteDecode = cryptoutil.LinearByteTable(sessionFormat.Basis)

var sessionFormat = cryptoutil.ReadAsset[struct {
	Basis      [8]byte
	ColumnMask [16]byte
}](formatAssets, "assets/session-key.bin")

func mixColumns(state [16]byte) [16]byte {
	var output [16]byte
	for i := 0; i < len(state); i += 4 {
		a, b, c, d := state[i], state[i+1], state[i+2], state[i+3]
		total := a ^ b ^ c ^ d
		output[i] = a ^ total ^ aesDouble(a^b)
		output[i+1] = b ^ total ^ aesDouble(b^c)
		output[i+2] = c ^ total ^ aesDouble(c^d)
		output[i+3] = d ^ total ^ aesDouble(d^a)
	}
	return output
}

func aesDouble(value byte) byte { return value<<1 ^ 0x1b*(value>>7) }
