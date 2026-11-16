//go:build linux && 386 && ishcrypto

package ish

import (
	"crypto/cipher"
	"crypto/subtle"
	"encoding/binary"
	"math/bits"
)

// ishAes folds each AES-256 round key into one of the shared tables, so the
// emulated rounds skip the key loads. The layout is fixed for the assembly:
// rounds two to thirteen use round, the final round uses byte-position tables
// that already store output byte order.
type ishAes struct {
	round  [12][4][256]uint32
	last   [4][4][256]uint32
	k0, k1 [4]uint32
	view   cipher.AesIsh
}

func newIshAes(view cipher.AesIsh) *ishAes {
	c := &ishAes{view: view}
	if view.Rounds != 14 {
		return c
	}
	c.k0, c.k1 = [4]uint32(view.Key[0:4]), [4]uint32(view.Key[4:8])
	for r := range c.round {
		for w := range 4 {
			k := view.Key[(r+2)*4+w]
			for x := range 256 {
				c.round[r][w][x] = te0[x] ^ k
			}
		}
	}
	for w := range 4 {
		for pos := range 4 {
			k := view.Key[56+w] & (0xff000000 >> (8 * pos))
			for x := range 256 {
				c.last[w][pos][x] = bits.ReverseBytes32(uint32(sbox0[x])<<(24-8*pos) ^ k)
			}
		}
	}
	return c
}

//go:noescape
func counterIsh256(tables *ishAes, destination, source *byte, count int, nonce *[16]byte)

// counterCryptIsh retains the standard AES key expansion and generic fallback.
func counterCryptIsh(c *ishAes, out, src []byte, counter *[16]byte) {
	if c.view.Rounds == 14 {
		n := len(src) / 16
		if n > 0 {
			counterIsh256(c, &out[0], &src[0], n, counter)
			binary.BigEndian.PutUint32(counter[12:], binary.BigEndian.Uint32(counter[12:])+uint32(n))
			out, src = out[n*16:], src[n*16:]
		}
	}
	var block [16]byte
	for len(src) > 0 {
		c.view.Encrypt(block[:], counter[:])
		n := subtle.XORBytes(out, src, block[:])
		binary.BigEndian.PutUint32(counter[12:], binary.BigEndian.Uint32(counter[12:])+1)
		out, src = out[n:], src[n:]
	}
}
