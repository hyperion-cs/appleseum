// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build linux && 386 && ishcrypto

package ish

// Unrolled multiplication using Thomas Pornin's BearSSL technique.
// https://www.bearssl.org/constanttime.html#ghash-for-gcm
func carryMulIsh(x, y uint32) uint64 {
	x0 := uint64(x & 0x11111111)
	x1 := uint64(x & 0x22222222)
	x2 := uint64(x & 0x44444444)
	x3 := uint64(x & 0x88888888)
	y0 := uint64(y & 0x11111111)
	y1 := uint64(y & 0x22222222)
	y2 := uint64(y & 0x44444444)
	y3 := uint64(y & 0x88888888)
	z0 := (x0*y0 ^ x1*y3 ^ x2*y2 ^ x3*y1) & 0x1111111111111111
	z1 := (x0*y1 ^ x1*y0 ^ x2*y3 ^ x3*y2) & 0x2222222222222222
	z2 := (x0*y2 ^ x1*y1 ^ x2*y0 ^ x3*y3) & 0x4444444444444444
	z3 := (x0*y3 ^ x1*y2 ^ x2*y1 ^ x3*y0) & 0x8888888888888888
	return z0 | z1 | z2 | z3
}

// multiplyIsh multiplies one block in the GHASH field. Unrolling the scalar
// products is important under iSH; general GHASH remains in standard crypto.
// Words are most-significant first, matching the lookup tables.
func multiplyIsh(out, H, input *[4]uint32) {
	y := [4]uint32{input[3], input[2], input[1], input[0]}
	h := [4]uint32{H[3], H[2], H[1], H[0]}
	var zLo, zHi, zSum [3]uint64

	zLo[0] = carryMulIsh(y[0], h[0])
	zHi[0] = carryMulIsh(y[1], h[1])
	zSum[0] = carryMulIsh(y[0]^y[1], h[0]^h[1])

	zLo[1] = carryMulIsh(y[2], h[2])
	zHi[1] = carryMulIsh(y[3], h[3])
	zSum[1] = carryMulIsh(y[2]^y[3], h[2]^h[3])

	zLo[2] = carryMulIsh(y[0]^y[2], h[0]^h[2])
	zHi[2] = carryMulIsh(y[1]^y[3], h[1]^h[3])
	zSum[2] = carryMulIsh((y[0]^y[2])^(y[1]^y[3]), (h[0]^h[2])^(h[1]^h[3]))

	var result [3][2]uint64
	for i := range 3 {
		mid := zSum[i] ^ zLo[i] ^ zHi[i]
		result[i][0] = zLo[i] ^ (mid << 32)
		result[i][1] = zHi[i] ^ (mid >> 32)
	}

	result[2][0] ^= result[0][0] ^ result[1][0]
	result[2][1] ^= result[0][1] ^ result[1][1]

	result[0][1] ^= result[2][0]
	result[1][0] ^= result[2][1]

	var z [4]uint64
	z[0] = result[0][0] << 1
	z[1] = (result[0][1] << 1) | (result[0][0] >> 63)
	z[2] = (result[1][0] << 1) | (result[0][1] >> 63)
	z[3] = (result[1][1] << 1) | (result[1][0] >> 63)

	for i := range 2 {
		lw := z[i]
		z[i+2] ^= lw ^ (lw >> 1) ^ (lw >> 2) ^ (lw >> 7)
		z[i+1] ^= (lw << 63) ^ (lw << 62) ^ (lw << 57)
	}

	*out = [4]uint32{uint32(z[3] >> 32), uint32(z[3]), uint32(z[2] >> 32), uint32(z[2])}
}
