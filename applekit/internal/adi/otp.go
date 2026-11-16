// Package adi implements IdMS/AMD provisioning, OTP and synchronization.
package adi

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha1"
	"embed"
	"encoding/binary"
	"errors"
	"io"
	"time"

	"github.com/hyperion-cs/appleseum/applekit/internal/cryptoutil"
)

//go:embed assets
var formatAssets embed.FS

// The AES block is shared; each OTP uses its own CBC state.
type otpGenerator struct {
	block      cipher.Block
	stream, iv [16]byte
	header     uint32
}

func newIdmsOtp(keyData [400]byte, streamData [32]byte) otpGenerator {
	key := deriveSoftwareKey(keyData)
	defer clear(key[:])
	block, _ := aes.NewCipher(key[:])
	stream, prefix := deriveSoftwareStream(streamData)
	return otpGenerator{block: block, stream: stream, iv: chainIv(block, prefix), header: 5}
}

// Cache the CBC state after the fixed, record-dependent prefix.
func newAmdOtp(keyData [272]byte) otpGenerator {
	key := deriveAmdKey(keyData)
	defer clear(key[:])
	block, _ := aes.NewCipher(key[:])
	return otpGenerator{block: block, stream: amdOtpFormat.Stream, iv: chainIv(block, amdOtpFormat.Prefix), header: 4}
}

func chainIv(block cipher.Block, prefix [64]byte) (iv [16]byte) {
	cipher.NewCBCEncrypter(block, iv[:]).CryptBlocks(prefix[:], prefix[:])
	copy(iv[:], prefix[48:])
	return
}

func (g otpGenerator) encrypt(counter uint32) (result [16]byte) {
	expanded := expandCounter(counter, g.stream)
	var digest [32]byte
	if g.header == 4 {
		digest = amdDigest(expanded[:])
	} else {
		digest = idmsDigest(expanded[:])
	}
	cipher.NewCBCEncrypter(g.block, g.iv[:]).CryptBlocks(digest[:], digest[:])
	copy(result[:], digest[16:])
	return
}

// IdMS appends a four-byte flag: its wrapper is 28 bytes, versus AMD's 24.
func (g otpGenerator) generate(counter uint32) []byte {
	value := g.encrypt(counter)
	out := binary.BigEndian.AppendUint32(make([]byte, 0, 28), g.header)
	out = binary.BigEndian.AppendUint32(out, aes.BlockSize)
	out = append(out, value[:]...)
	if g.header == 5 {
		out = binary.BigEndian.AppendUint32(out, 1)
	}
	return out
}

func (g otpGenerator) synchronize(sim []byte, at time.Time, entropy io.Reader) ([]byte, error) {
	if len(sim) != 32 || binary.BigEndian.Uint32(sim) != 4 {
		return nil, errors.New("adi: invalid Sync server message")
	}
	if !otpTime(at) {
		return nil, errors.New("adi: invalid Sync time")
	}
	if g.header != 4 && g.header != 5 {
		return nil, errors.New("adi: unsupported Sync record")
	}
	second := g.encrypt(binary.BigEndian.Uint32(sim[8:12]))
	out := make([]byte, 0, 121)
	out = append(out, g.generate(uint32(at.Unix() / 30))[:24]...)
	out = binary.BigEndian.AppendUint32(out, 16)
	out = append(out, second[:]...)
	out = binary.BigEndian.AppendUint32(out, uint32(len(sim)))
	out = append(out, sim...)
	out = binary.BigEndian.AppendUint32(out, uint32(at.Unix()))
	if g.header == 5 {
		out = binary.BigEndian.AppendUint32(out, 1)
	}
	checksum := syncChecksum(out[4:])
	out = append(out, checksum[:]...)
	out = append(out, make([]byte, 13)...)
	if err := cryptoutil.ReadPadding(entropy, out[len(out)-13:], 0); err != nil {
		clear(out)
		return nil, err
	}
	return out, nil
}

// The stream is derived from the record, not its raw provisioning key.
func expandCounter(counter uint32, stream [16]byte) (block [55]byte) {
	for i, modulus := range counterModuli {
		binary.BigEndian.PutUint16(block[2*i:], uint16(counter%modulus))
	}
	accumulator := byte(0x93)
	for i := 0; i < 96; i++ {
		position := (7 + i) % len(block)
		block[position] ^= stream[i%len(stream)] + accumulator
		accumulator ^= block[position] * 0x44
	}
	return block
}

var counterModuli = cryptoutil.ReadAsset[[4]uint32](formatAssets, "assets/counter-moduli.bin")

var amdOtpFormat = cryptoutil.ReadAsset[struct {
	Prefix [64]byte
	Stream [16]byte
}](formatAssets, "assets/amd-otp.bin")

var syncChecksumCipher = cryptoutil.Aes128(cryptoutil.ReadAsset[[16]byte](formatAssets, "assets/sync-checksum.aes128.key"))

// Encrypt the first SHA-1 block; its remaining four bytes stay clear.
func encryptedSha1(block cipher.Block, message []byte) [sha1.Size]byte {
	digest := sha1.Sum(message)
	block.Encrypt(digest[:16], digest[:16])
	return digest
}

func syncChecksum(message []byte) [sha1.Size]byte { return encryptedSha1(syncChecksumCipher, message) }
