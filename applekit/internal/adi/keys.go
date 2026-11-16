package adi

import (
	"crypto/subtle"
	"math/bits"

	"github.com/hyperion-cs/appleseum/applekit/internal/cryptoutil"
)

// CBC-wrapped key expansion with byte mappings; only the original AES-128 key is needed.
type encryptedKeyParameters struct {
	Cipher  cryptoutil.CbcParameters
	Mapping maskedKeyFormat
}

type maskedKeyFormat struct {
	Positions, Masks, Mappings [4][8]byte
	Whitening                  [16]byte
}

func (f *maskedKeyFormat) importKey(masked [16]byte, table []byte) (key [16]byte) {
	var columns [4][8]byte
	for word := range columns {
		for bit, position := range f.Positions[word] {
			columns[word][bit] = bits.RotateLeft8(table[position]^f.Masks[word][bit], bit)
		}
	}
	for i := range key {
		word := 3 - i%4
		decoded := cryptoutil.ApplyByteMap(masked[i]^f.Whitening[i], columns[word])
		key[i] = cryptoutil.ApplyByteMap(decoded, f.Mappings[word])
	}
	return
}

var amdKeyFormat = cryptoutil.ReadAsset[encryptedKeyParameters](formatAssets, "assets/amd-key.bin")
var amdFieldCipher = cryptoutil.NewCbc(amdKeyFormat.Cipher)

// The masked key is split across expansion blocks 0 and 11.
func deriveAmdKey(encrypted [272]byte) [16]byte {
	amdFieldCipher.Decrypt(encrypted[:], encrypted[:])
	defer clear(encrypted[:])
	var masked [16]byte
	subtle.XORBytes(masked[:], encrypted[:16], encrypted[176:192])
	return amdKeyFormat.Mapping.importKey(masked, encrypted[208:])
}

var materialFormat = cryptoutil.ReadAsset[struct {
	Cipher      cryptoutil.CbcParameters
	DecodeBasis [8]byte
	KeyMask     [16]byte
	Sources     [256]uint16
}](formatAssets, "assets/material-key.bin")
var softwareMaterialCipher = cryptoutil.NewCbc(materialFormat.Cipher)

var softwareMaterialByteDecode = cryptoutil.LinearByteTable(materialFormat.DecodeBasis)

func deriveSoftwareKey(encrypted [400]byte) [16]byte {
	softwareMaterialCipher.Decrypt(encrypted[:], encrypted[:])
	return deriveSoftwareKeyClear(encrypted)
}

// Each key byte combines two selections, cancelling the intermediate XOR mask.
func deriveSoftwareKeyClear(clear [400]byte) (key [16]byte) {
	state := clear[14] + clear[290] + clear[292] + clear[319]
	for step, source := range materialFormat.Sources {
		value := clear[source]
		switch state & 7 {
		case 0:
			value -= state
		case 1:
			value += state
		case 2, 3:
			value ^= state
		}
		if (step >= 64 && step < 80) || step >= 240 {
			key[step%len(key)] ^= value
		}
		state = (state ^ value) + 18
	}
	for i, value := range key {
		key[i] = softwareMaterialByteDecode[value] ^ materialFormat.KeyMask[i]
	}
	return cryptoutil.ReverseAes128Key(key, 1)
}

var streamFormat = cryptoutil.ReadAsset[struct{ Store, Wrap cryptoutil.CbcParameters }](formatAssets, "assets/stream-ciphers.bin")
var softwareStreamCipher = cryptoutil.NewCbc(streamFormat.Store)
var provisioningStreamCipher = cryptoutil.NewCbc(streamFormat.Wrap)

// Record decoding removes the outer Store cipher, leaving the provisioning Wrap layer.
func encryptProvisioningStream(input [32]byte) (out [32]byte) {
	provisioningStreamCipher.Encrypt(out[:], input[:])
	softwareStreamCipher.Encrypt(out[:], out[:])
	return
}

// The second prefix hash permutes and repeats the first 32-byte digest.
func deriveSoftwareStream(data [32]byte) (stream [16]byte, prefix [64]byte) {
	softwareStreamCipher.Decrypt(data[:], data[:])
	for i := range stream {
		stream[i] = data[2*i+1]
	}
	first := idmsDigest(data[:])
	var mixed [64]byte
	for i := range first {
		mixed[i] = first[(5*i+7)%32] ^ (first[(5*i*i+9)%32] + 42)
	}
	copy(mixed[32:], mixed[:32])
	second := idmsDigest(mixed[:])
	copy(prefix[:32], first[:])
	copy(prefix[32:], second[:])
	return
}
