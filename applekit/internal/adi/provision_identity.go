package adi

import (
	"math/bits"

	"github.com/hyperion-cs/appleseum/applekit/internal/cryptoutil"
)

type uuidSubstitutionParameters struct {
	Left, Right, Lookup [64]byte
	Seed                [32]byte
}

var provisioningIdentityFormat = cryptoutil.ReadAsset[struct {
	Key          [16]byte
	First, Final uuidSubstitutionParameters
}](formatAssets, "assets/provisioning-identity.bin")
var provisioningIdentityCipher = cryptoutil.Aes128(provisioningIdentityFormat.Key)

func provisioningMachineId(uuid [16]byte) [20]byte {
	message, length := provisioningUuidMessage(uuid)
	return encryptedSha1(provisioningIdentityCipher, message[:length])
}

func provisioningUuidMessage(uuid [16]byte) (message [60]byte, length int) {
	n := 153 + 61*cyclicSum(uuid[:], 139)
	var storage [256]byte
	buffer := storage[:n%177+80]
	state := provisioningUuidState(uuid[:])
	provisioningUuidFirst(buffer, &state)
	// Three buffer lengths cover lag 177 even at the minimum length 80.
	cryptoutil.MixOutput(buffer, uint32(3*len(buffer)))
	m := 14 * cyclicSum(buffer, len(buffer)+172) % 41
	state = provisioningUuidState(buffer)
	length = m + 20
	payload := message[:length]
	provisioningUuidFinal(payload, &state)
	// The final pass uses uint32 subtraction before taking the remainder.
	cryptoutil.MixOutput(payload, 0)
	return
}

func cyclicSum(src []byte, count int) (sum int) {
	for _, value := range src {
		sum += int(value)
	}
	sum *= count / len(src)
	for _, value := range src[:count%len(src)] {
		sum += int(value)
	}
	return
}

func provisioningUuidState(src []byte) (state [384]byte) {
	cryptoutil.Repeat(state[:], src)
	cryptoutil.MixState(state[:])
	return
}

func provisioningUuidFold(dst []byte, fill byte, seed *[32]byte, left, right *[64]byte, state *[384]byte) {
	for i := range dst {
		dst[i] = fill
	}
	for i, v := range seed {
		dst[i%len(dst)] += v
	}
	cryptoutil.XorFold(dst, left[:], right[:], state[:])
}

// Table reads see preceding writes; preserve statement order.
func provisioningUuidFirst(dst []byte, state *[384]byte) {
	p := &provisioningIdentityFormat.First
	left, right, seed := p.Left, p.Right, p.Seed

	l := func(i int) byte { return left[state[i]&63] }

	r := func(i int) byte { return right[state[i]&63] }

	// First.Lookup includes the high-bit bias used by its substitutions.

	t := func(i int) byte { return p.Lookup[state[i]&63] }

	pick, majority, rotate := cryptoutil.Select, cryptoutil.Majority, cryptoutil.Rotate

	state[174] ^= cube(l(224))&190 | 1
	state[243] += 100
	right[6] = -(3 * state[210]) - 3
	right[27] = ^pick(53, l(249), state[57])
	left[31] = 244
	seed[20] = 18*(l(48)&14) ^ 146
	left[7] = 167
	right[52] = 15 + (majority(state[18], cube(state[92]), 2) & 162) + (state[92] & 1)
	state[195] = (state[195] - (3*state[210] ^ (l(224)&29 | r(172)&226))) ^ 20
	left[41] = t(146) & 164
	left[16] = ((r(63) & 107) ^ 105) - 119
	right[3] = l(121)&201 | 34
	left[6] = rotate(state[170], -majority(l(124), l(109), 5))
	left[52] = r(241) / 3
	left[47] = 104
	square := l(65)
	square *= square
	state[97] ^= 128 ^ pick(left[16]^32, square&57|70, square&68|32)
	state[118] = pick(56, r(119), 3*l(152))
	left[17] = 31
	right[43] = 143 - (r(18) & 79)
	state[93] -= 120
	state[150] += state[93]*state[93] | 3
	right[48] = (l(26)&146 ^ 128 | t(73)&178) + 83
	left[37] = (state[14] & 180) - 76
	left[16] += (state[23]*r(247))&224 | 31
	left[33] = ^(state[232]/3)&23 | 224
	left[29] = (r(49)^8)&200 | 38
	left[14] = 69 - cube(l(138))
	left[55] = 54
	right[40] = 145 - 2*(t(138)&18)
	seed[9] = left[state[150]&63] - 39
	right[1] = 4 * r(244)
	left[11] = state[189] + t(6) + 103
	state[159] -= 90
	state[72] += (r(112) ^ 207) | 16
	state[156] -= 82
	left[24] = 110
	state[52] = (2*r(192))&150 | 72
	left[59] = 236
	state[207] += majority(r(139), l(128)+state[82], 178)
	state[87] += 44
	state[59] = pick(state[203], t(166)&98, t(166)&4|234)
	right[50] = (r(77) & 206) - 61
	left[53] = ((161 - state[122]) & 107) + 7
	right[58] = 2 * pick(right[40], state[110], r(251))
	left[59] = 167
	mask := bits.RotateLeft8(r(162), 6)&215 ^ 128
	left[26] = ((r(108)&144)-57)&mask - 120
	state[44] += 114
	seed[13] = 19 - majority(bits.RotateLeft8(state[211], 5), l(64), 68)
	wide := uint16(l(102))
	left[27] = byte(wide*wide/2) ^ 53
	seed[8] = 119 - left[state[150]&63]
	state[68] += (state[113] & 128) - (t(148) & 83) - (t(113) & 44)
	left[45] = (l(202) ^ state[154]/3) + 51
	right[24] = t(188)&75 | 180
	right[1] += 15
	seed[23] = (state[190]&244 ^ 52) + (^right[6] & 9)
	provisioningUuidFold(dst, 0xdc, &seed, &left, &right, state)
}

func provisioningUuidFinal(dst []byte, state *[384]byte) {
	p := &provisioningIdentityFormat.Final
	left, right, seed := p.Left, p.Right, p.Seed

	l := func(i int) byte { return left[state[i]&63] }

	r := func(i int) byte { return right[state[i]&63] }

	t := func(i int) byte { return p.Lookup[state[i]&63] }

	pick, majority, rotate := cryptoutil.Select, cryptoutil.Majority, cryptoutil.Rotate

	state[141] ^= state[235]
	state[90] += 90
	state[193] += l(210)&170 | 68
	seed[17] = ((l(221) - l(255)&61) ^ 86) + ((r(190)&99 | t(162)&12) ^ 73) - 127
	state[103] ^= r(191)&64 | 158
	right[13] = 62
	right[25] = t(112) ^ 189
	left[26] = (l(225)&10|128)/3 + 8
	right[7] = 171
	state[235] -= 19
	state[58] -= state[164]
	right[39] = 114
	right[37] = r(115)&238 ^ 32
	state[172] -= 88
	left[24] = bits.RotateLeft8(l(42), 6) + 52
	right[44] = majority(l(170)|t(70), t(110)/5, 151) ^ 99
	right[5] = cube(state[14])
	state[43] = t(11)
	left[63] = ((r(218) - 110) ^ 140) - 97
	right[37] = 7
	right[51] = 116
	state[72] ^= l(238) + (r(59)&146 ^ 18) - 54
	right[17] = (right[5] & 149) - 12
	left[43] = 244 - (l(3) & 88) - (l(168) & 2)
	right[49] = (r(60)-18)&38 | 200
	right[63] = 236 - ((right[seed[17]&63] + 13) & 145)
	left[29] = state[240]
	left[60] = t(10)
	state[151] = rotate(state[233], state[114])&176 | l(60)&11 | 68
	right[34] = (state[168]&167)*(l(24)&86|129) ^ 206
	right[2] = pick(44, state[63], r(163))*189 + 78
	left[53] = (t(31) & 144) - 127
	state[38] = 14
	square := r(120)&35 | 88
	state[171] ^= square * square
	state[243] = (state[211]&56 | 64) & (2 * state[243])
	state[245] -= 112
	state[19] += (state[23]&22 | 72) / 5
	left[18] = 55
	left[28] = 25 - ((t(142) / 2) & 22)
	left[24] = (t(176)/2)&123 | state[79]&107 | 4
	state[221] = ^majority(l(224), l(6), 187)
	state[2] -= t(213)
	left[28] ^= 111
	right[35] = 72 * (state[103] / 2)
	left[12] = pick(111, l(37), t(49)) / 3
	state[70] -= state[177]
	right[11] = (r(147) & 145) - 116
	left[53] += pick(seed[17], rotate(39, state[141]), right[49]&59|132)
	right[39] += state[194]
	state[200] ^= rotate(left[26]&12|178, state[14])
	right[38] = (state[133] & 171) - 114
	right[42] = cube(l(118))&115 | 140
	left[42] = 161 - rotate(state[2]&9|182, state[248])
	seed[16] = 2*(majority(left[(state[63]/2+30)&63], right[42], 2)&115) + 25
	seed[24] = (state[47] & 64) + 35
	seed[28] = state[63]/2 + 94
	provisioningUuidFold(dst, 0x31, &seed, &left, &right, state)
}

func cube(value byte) byte { return value * value * value }
