package kbsync

import (
	"crypto/rand"
	"encoding/binary"
	"math/bits"

	"github.com/hyperion-cs/appleseum/applekit/internal/cryptoutil"
)

const (
	scalarPayloadBits        = 27
	scalarPayloadMask uint32 = 0x07ffffff
)

func bodyInputs() ([144]byte, [12]byte) {
	var payload [52]byte
	var suffix [16]byte
	var nonce [12]byte
	randomNonzero(payload[:])
	randomNonzero(suffix[:])
	randomNonzero(nonce[:])
	binary.BigEndian.PutUint32(payload[:4], binary.BigEndian.Uint32(payload[:4])/2+1)
	values := [...][]byte{{2}, payload[:], {1}, {1}, {0, 0, 0, 2}}
	var message [144]byte
	out := append(message[:0], bodyFraming.Header[:]...)
	out = append(out, 1)
	out = appendScalar(out, uint32(len(values)))
	for i, value := range values {
		out = binary.BigEndian.AppendUint32(out, bodyFraming.Tags[i])
		out = appendScalar(out, uint32(len(value)))
		out = append(out, value...)
	}
	out = append(out, suffix[:]...)
	return [144]byte(out), nonce
}

func randomNonzero(dst []byte) {
	rand.Read(dst)
	for i := range dst {
		for dst[i] == 0 {
			rand.Read(dst[i : i+1])
		}
	}
}

func appendScalar(dst []byte, value uint32) []byte {
	var noise [4]byte
	rand.Read(noise[:])
	return binary.BigEndian.AppendUint32(dst, encodeScalar(value, binary.LittleEndian.Uint32(noise[:])))
}

func encodeScalar(value, noise uint32) uint32 {
	width := max(1, bits.Len32(value))
	valueMask := uint32(1)<<width - 1
	return uint32(width)<<scalarPayloadBits | noise&scalarPayloadMask&^valueMask | value
}

var bodyFraming = cryptoutil.ReadAsset[struct {
	Header [24]byte
	Tags   [5]uint32
}](formatAssets, "assets/body-framing.bin")
