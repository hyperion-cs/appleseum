package kbsync

import "github.com/hyperion-cs/appleseum/applekit/internal/cryptoutil"

const (
	mixerFirstBias  byte = 161
	mixerSecondBias byte = 59
	mixerHeadBias   byte = 217
	mixerTailBias   byte = 104
)

var mixerFormat = cryptoutil.ReadAsset[struct {
	Left, Table, Right [64]byte
}](formatAssets, "assets/mixer.bin")

func bodyMixer(input [26]byte) [16]byte {
	var state [319]byte
	cryptoutil.Repeat(state[:], input[:])
	cryptoutil.MixState(state[:])
	out := substituteBodyState(&state)
	cryptoutil.MixOutput(out[:], 0)
	return out
}

// Indexed reads see preceding table writes; preserve statement order.
func substituteBodyState(state *[319]byte) (out [16]byte) {
	left, right := mixerFormat.Left, mixerFormat.Right
	table := &mixerFormat.Table

	l := func(i int) byte { return left[state[i]&63] }

	r := func(i int) byte { return right[state[i]&63] }

	t := func(i int) byte { return table[state[i]&63] }

	pick, majority := cryptoutil.Select, cryptoutil.Majority

	rotateRight := func(value, count byte) byte { return cryptoutil.Rotate(value, -count) }

	selected := r(18)
	left[7] = (state[25]/2)&67 | 60
	lookup := (selected*selected - 52) ^ 60
	left[55] = lookup&(state[138]|185) | state[138]&176
	selector := 2*state[150]*state[150] ^ 24
	right[15] -= pick(selector, l(179), state[237])
	square := r(195)
	square *= square
	right[47] += square * square
	// lookup&63 cannot be 7 or 55, so the rewritten entries are unreachable.
	indexSeed := mixerFormat.Left[lookup&63]
	indexSeed = 3*indexSeed*indexSeed + 54
	right[0] = 17
	right[23] += state[174]
	left[61] = 236
	// Table[43] is contained in Table[24], so the outer Majority is redundant.
	left[12] += table[43] | majority(state[20], state[234], left[52])&(l(167)|table[24])
	right[43] += (r(201)/3)&63 | l(171)&64
	selectedRight := r(196)
	right[32] += state[89]
	selection := majority(majority(state[200], state[202], right[8]), left[39], state[112]+t(33)) ^ 67
	state[70] += majority(24*state[174], right[selection&63]+left[24], table[28])
	state[58] = (state[154] + 24) | 24
	state[230] -= 42
	left[19] = l(38)
	left[25] -= state[156]
	right[26] -= (t(173) | 24) + t(153)&214
	left[41] = state[240] * 182
	state[192] = right[lookup&63]&144 | 3
	left[32] = 71
	right[34] = 34
	left[33] = 204
	state[104] += rotateRight(state[249]^selector, t(217))&240 | l(253)&10
	right[48] = 242
	state[222] -= majority(state[80], r(3), 136) * pick(24, r(163), left[(24-state[128])&63])
	left[51] = 135
	left[7] = 226
	right[8] = 204 + (state[232] & 165)
	choice := pick(left[58], selectedRight, t(117)) ^ 24 | 68
	right[26] ^= majority(l(8), state[23], 163) + state[67]&choice
	state[189] += (t(98)&120 | 129) ^ (l(25) & r(61))
	x := l(55) ^ 1
	right[62] += x * x
	addressMask := 125 & (state[218] / 3)
	left[48] -= pick(left[44], l(202)|24, table[(57-addressMask)&63])
	state[22] -= state[87]
	state[76] ^= majority(l(200)/2, right[62], 20)
	right[37] -= left[indexSeed&63]
	state[253] += 102 ^ (state[75] & 190)
	right[23] = state[148]
	right[50] ^= majority(t(168), state[161], left[12]) - pick(left[18], t(252), table[3])
	right[57] -= t(236)
	left[18] = 236 ^ (state[41] * state[235] & 133)
	left[10] -= state[30]
	right[18] -= l(116)
	state[78] += right[(24-state[128])&63]&122 | 1
	state[160] ^= table[state[36]&30|32]
	state[103] += 81
	left[23] ^= right[57]/3 | t(211)
	right[28] = 248

	for i := range 8 {
		out[i], out[i+8] = mixerFirstBias, mixerSecondBias
	}
	out[0] = (state[249] ^ selector) + mixerHeadBias
	out[1] = selection + 94
	out[3] = mixerFirstBias - state[128] - t(29)/15
	out[5] = choice + 157
	out[9] = 246
	out[10] = mixerSecondBias - rotateRight(189, t(185)&5|l(120)&2)
	out[11] = mixerSecondBias - addressMask
	out[13] = state[130] + mixerSecondBias
	out[15] = indexSeed + lookup + mixerTailBias
	cryptoutil.XorFold(out[:], left[:], right[:], state[:])
	return
}
