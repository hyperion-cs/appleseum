package sapkey

import (
	"crypto/subtle"
	"io"

	"github.com/hyperion-cs/appleseum/applekit/internal/cryptoutil"
)

const (
	credentialByteMask byte = 0x2d
)

// Type-1 Feistel transform: two independently padded blocks with nonzero padding.
func buildConfiguratorCredential(entropy io.Reader) ([256]byte, error) {
	var out [256]byte
	for block, prefix := range [][]byte{credentialFormat.Prefix0[:], credentialFormat.Prefix1[:]} {
		data := out[block*128 : (block+1)*128]
		copy(data, prefix)
		if err := cryptoutil.ReadPadding(entropy, data[len(prefix):], credentialByteMask); err != nil {
			return [256]byte{}, err
		}
		for round := 0; round < 7; round++ {
			f := configuratorCredentialF((*[64]byte)(data[64:]))
			subtle.XORBytes(f[:], data[:64], f[:])
			copy(data[:64], data[64:])
			copy(data[64:], f[:])
			clear(f[:])
		}
	}
	return out, nil
}

func configuratorCredentialF(input *[64]byte) (out [64]byte) {
	var s [80]byte
	for i, value := range input {
		s[i] = credentialInput[value+byte(i)] + byte(i)
	}
	copy(s[64:], credentialFormat.InitialTail[:])
	// The last pass only writes bytes that reach the output.
	for pass, width := range [...]int{80, 80, 80, 64} {
		for i := range width {
			s[i] ^= credentialFeedback5[s[(i+75)%80]] +
				credentialFeedback25[s[(i+55)%80]] + byte(81*pass+i)
		}
	}
	for i := range out {
		out[i] = credentialOutput[s[i]] + byte(i)
	}
	clear(s[:])
	return
}

// Credential prefixes, tail and byte encodings around the AES S-box.
var credentialFormat = cryptoutil.ReadAsset[struct {
	Prefix0                              [68]byte
	Prefix1                              [110]byte
	InitialTail                          [16]byte
	Input, Feedback5, Feedback25, Output credentialSubstitution
}](formatAssets, "assets/credential.bin")

type credentialSubstitution struct {
	InputMask, OutputMask, Bias byte
}

func (p credentialSubstitution) expand() (table [256]byte) {
	for i := range table {
		value := cryptoutil.AesSubByte(byte(i) ^ p.InputMask)
		table[i] = (value ^ p.OutputMask) + p.Bias
	}
	return
}

var credentialInput = credentialFormat.Input.expand()
var credentialFeedback5 = credentialFormat.Feedback5.expand()
var credentialFeedback25 = credentialFormat.Feedback25.expand()
var credentialOutput = credentialFormat.Output.expand()
