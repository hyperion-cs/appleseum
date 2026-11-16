package kbsync

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/binary"

	"github.com/hyperion-cs/appleseum/applekit/internal/cryptoutil"
)

var bodyFormat = cryptoutil.ReadAsset[struct {
	Cipher           cryptoutil.CbcParameters
	KeyMask, MacMask [16]byte
	Salt             [255]byte
}](formatAssets, "assets/body-crypto.bin")
var bodyKeyBlock = cryptoutil.Aes128(bodyFormat.Cipher.Key)

func deriveBodyKey(hw [6]byte, nonce [12]byte, dsid uint64) (key [16]byte) {
	var buf [263]byte
	copy(buf[8:], bodyFormat.Salt[:])
	subtle.XORBytes(buf[8:14], buf[8:14], hw[:])
	subtle.XORBytes(buf[14:26], buf[14:26], nonce[:])
	for i := 0; i < 14; i++ {
		bias := [...]byte{61, 53, 57, 57}[i%4]
		if i >= 9 {
			bias += 32
		}
		buf[8+i] += bias
	}
	mixed := bodyMixer([26]byte(buf[8:34]))
	copy(buf[8:], mixed[:])
	binary.BigEndian.PutUint64(buf[:8], dsid)
	digest := md5.Sum(buf[:])
	subtle.XORBytes(digest[:], digest[:], bodyFormat.KeyMask[:])
	bodyKeyBlock.Encrypt(key[:], digest[:])
	return
}

// Wire body: header, nonce, AES-CBC ciphertext, HMAC-SHA1.
func sealBody(message [144]byte, nonce [12]byte, key [16]byte) (blob [196]byte) {
	binary.BigEndian.PutUint32(blob[:4], 0x00040003)
	copy(blob[4:16], nonce[:])
	var plaintext [160]byte
	binary.BigEndian.PutUint32(plaintext[:4], 144)
	copy(plaintext[4:], message[:])
	block, _ := aes.NewCipher(key[:])
	cipher.NewCBCEncrypter(block, bodyFormat.Cipher.Iv[:]).CryptBlocks(blob[16:176], plaintext[:])
	subtle.XORBytes(key[:], key[:], bodyFormat.MacMask[:])
	tag := hmac.New(sha1.New, key[:])
	tag.Write(blob[:176])
	copy(blob[176:], tag.Sum(nil))
	return
}
