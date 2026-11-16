// Package mt19937 twists all 624 words on every draw.
// A buffered MT generator or math/rand would produce a different stream.
package mt19937

import "encoding/binary"

// Index wraps as uint32, not at 624.
type Generator struct {
	State [624]uint32
	Index uint32
}

type SeedParameters struct {
	Seed uint32
	Key  [4]uint32
	Mask uint32
}

// NewGenerator uses init_by_array with the format's sparse XOR mask.
// The mask repeats Mask in rounds 1..3 and Mask-1 in round 4 of each eight.
func (p SeedParameters) NewGenerator() Generator {
	mask := [8]uint32{1: p.Mask, 2: p.Mask, 3: p.Mask, 4: p.Mask - 1}
	var g Generator
	s := &g.State
	s[0] = p.Seed
	for i := 1; i < len(s); i++ {
		s[i] = 1_812_433_253*(s[i-1]^s[i-1]>>30) + uint32(i)
	}
	i := 1

	advance := func() {
		if i++; i == len(s) {
			s[0], i = s[len(s)-1], 1
		}
	}

	for k := range len(s) {
		s[i] = (s[i] ^ (s[i-1]^s[i-1]>>30)*1_664_525) + (p.Key[k%4] ^ mask[k%8]) + uint32(k%4)
		advance()
	}
	for range len(s) - 1 {
		s[i] = (s[i] ^ (s[i-1]^s[i-1]>>30)*1_566_083_941) - uint32(i)
		advance()
	}
	s[0] = 0x80000000
	return g
}

func (g *Generator) Next() uint32 {
	state := &g.State
	// The second loop must read words already updated by the first.
	for i := 0; i < 227; i++ {
		y := state[i]&0x80000000 | state[i+1]&0x7fffffff
		state[i] = state[i+397] ^ y>>1 ^ (0x9908b0df * (y & 1))
	}
	for i := 227; i < 623; i++ {
		y := state[i]&0x80000000 | state[i+1]&0x7fffffff
		state[i] = state[i-227] ^ y>>1 ^ (0x9908b0df * (y & 1))
	}
	y := state[623]&0x80000000 | state[0]&0x7fffffff
	state[623] = state[396] ^ y>>1 ^ (0x9908b0df * (y & 1))
	x := state[g.Index%624]
	g.Index++
	x ^= x >> 11
	x ^= x << 7 & 0x9d2c5680
	x ^= x << 15 & 0xefc60000
	return x ^ x>>18
}

// Fill backwards from low draw bytes, XORed with the destination mask.
// A rejected zero still consumes its byte from the draw.
func (g *Generator) FillNonzero(dst, mask []byte) {
	var raw [4]byte
	for n := len(dst); n > 0; {
		binary.LittleEndian.PutUint32(raw[:], g.Next())
		for _, value := range raw {
			if b := value ^ mask[n-1]; b != 0 {
				n--
				dst[n] = b
				if n == 0 {
					return
				}
			}
		}
	}
}
