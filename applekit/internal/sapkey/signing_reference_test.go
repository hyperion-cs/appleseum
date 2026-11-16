package sapkey

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"errors"
	"testing"
)

func TestDeriveConfiguratorEnvelope(t *testing.T) {
	var state ConfiguratorRequestState
	for index := range state.material[:16] {
		state.material[index] = byte(index + 1)
	}
	for index := range state.credential {
		state.credential[index] = byte(index)
	}
	for index := range state.envelopeRandom {
		state.envelopeRandom[index] = byte(0x80 + index)
	}
	state.envelopeRandom[32] = 5

	plaintext := make([]byte, 64)
	var responseKey [16]byte
	for field := 0; field < 3; field++ {
		offset := field * 20
		binary.BigEndian.PutUint32(plaintext[offset:], 16)
		for index := 0; index < 16; index++ {
			plaintext[offset+4+index] = byte(field*16 + index)
		}
		if field == 2 {
			copy(responseKey[:], plaintext[offset+4:offset+20])
		}
	}
	reply := make([]byte, 21+len(plaintext)+3)
	reply[0] = 2
	for index := range reply[1:17] {
		reply[1+index] = byte(0x30 + index)
	}
	binary.BigEndian.PutUint32(reply[17:21], uint32(len(plaintext)))
	block, err := aes.NewCipher(state.material[:16])
	if err != nil {
		t.Fatal(err)
	}
	cipher.NewCBCEncrypter(block, reply[1:17]).CryptBlocks(reply[21:21+len(plaintext)], plaintext)
	copy(reply[21+len(plaintext):], []byte{7, 8, 9})

	envelope, err := DeriveConfiguratorEnvelope(state, reply)
	if err != nil {
		t.Fatal(err)
	}
	if len(envelope) != 468 || !bytes.Equal(envelope[:13], []byte("\x00\x00\x01\xd0\x03\x00\x00\x00\x01\x00\x00\x01\x00")) {
		t.Fatal("invalid Configurator envelope framing")
	}
	if !bytes.Equal(envelope[13:269], state.credential[:]) || binary.BigEndian.Uint32(envelope[269:273]) != 21 {
		t.Fatal("invalid Configurator credential or variable length")
	}
	wantRandom := state.envelopeRandom
	block, err = aes.NewCipher(responseKey[:])
	if err != nil {
		t.Fatal(err)
	}
	block.Encrypt(wantRandom[:16], wantRandom[:16])
	if !bytes.Equal(envelope[273:294], wantRandom[:21]) {
		t.Fatal("invalid Configurator variable field")
	}
	if binary.BigEndian.Uint32(envelope[294:298]) != 3 || !bytes.Equal(envelope[298:301], []byte{7, 8, 9}) {
		t.Fatal("invalid Configurator footer")
	}
}

func TestDeriveConfiguratorTagRejectsShortState(t *testing.T) {
	var state ConfiguratorRequestState
	reply := make([]byte, 21+64)
	binary.BigEndian.PutUint32(reply[17:21], 64)
	block, err := aes.NewCipher(state.material[:16])
	if err != nil {
		t.Fatal(err)
	}
	cipher.NewCBCEncrypter(block, reply[1:17]).CryptBlocks(reply[21:], make([]byte, 64))
	if _, err := DeriveConfiguratorTag(state, reply, make([]byte, 16), make([]byte, 468), nil); err == nil {
		t.Fatal("short Configurator tag state was accepted")
	}
}

func DeriveConfiguratorEnvelope(state ConfiguratorRequestState, reply []byte) ([]byte, error) {
	plaintext, footer, err := decryptConfiguratorReply(state, reply)
	if err != nil {
		return nil, err
	}
	defer clear(plaintext)
	return configuratorEnvelope(state, plaintext, footer)
}

func DeriveConfiguratorTag(state ConfiguratorRequestState, reply, nonce, envelope, body []byte) ([]byte, error) {
	if len(nonce) != 16 || len(envelope) < 4 {
		return nil, errors.New("sapkey: invalid Configurator nonce or envelope")
	}
	plaintext, _, err := decryptConfiguratorReply(state, reply)
	if err != nil {
		return nil, err
	}
	defer clear(plaintext)
	if len(plaintext) < 0x4c4 {
		return nil, errors.New("sapkey: truncated Configurator tag state")
	}
	key := sessionKey(plaintext[0x284:0x4c4])
	block, _ := aes.NewCipher(key[:])
	out := make([]byte, 16)
	writeTag(out, block, nonce, envelope, body)
	return out, nil
}
