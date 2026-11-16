package cryptoutil

import (
	"crypto/aes"
	"crypto/cipher"
	"io"
)

type CbcParameters struct {
	Key, Iv [aes.BlockSize]byte
}

func Aes128(key [aes.BlockSize]byte) cipher.Block {
	block, _ := aes.NewCipher(key[:])
	return block
}

// CBC shares its immutable AES block; each call creates its own chaining state.
type Cbc struct {
	block cipher.Block
	iv    [aes.BlockSize]byte
}

func NewCbc(p CbcParameters) Cbc { return Cbc{Aes128(p.Key), p.Iv} }

func (c Cbc) Encrypt(dst, src []byte) { cipher.NewCBCEncrypter(c.block, c.iv[:]).CryptBlocks(dst, src) }

func (c Cbc) Decrypt(dst, src []byte) { cipher.NewCBCDecrypter(c.block, c.iv[:]).CryptBlocks(dst, src) }

// AesSubByte applies the standard AES S-box.
func AesSubByte(value byte) byte { return aesParameters.Sbox[value] }

// Recover the original AES-128 key from a round key; crypto/aes has no key-schedule import.
func ReverseAes128Key(key [16]byte, round int) [16]byte {
	if round < 0 || round > 10 {
		panic("cryptoutil: invalid AES-128 round")
	}
	rcon := aesParameters.Rounds
	for ; round > 0; round-- {
		for i := 15; i >= 4; i-- {
			key[i] ^= key[i-4]
		}
		last := [4]byte{key[13], key[14], key[15], key[12]}
		for i, value := range last {
			key[i] ^= aesParameters.Sbox[value]
		}
		key[0] ^= rcon[round-1]
	}
	return key
}

// Reject zero source bytes before applying the output mask.
func ReadPadding(entropy io.Reader, dst []byte, mask byte) error {
	if _, err := io.ReadFull(entropy, dst); err != nil {
		return err
	}
	for i := range dst {
		for dst[i] == 0 {
			if _, err := io.ReadFull(entropy, dst[i:i+1]); err != nil {
				return err
			}
		}
		dst[i] ^= mask
	}
	return nil
}

var aesParameters = ReadAsset[struct {
	Sbox   [256]byte
	Rounds [10]byte
}](formatAssets, "assets/aes128.bin")
