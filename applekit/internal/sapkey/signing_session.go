package sapkey

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/binary"
	"errors"
)

const (
	configuratorSigningStateStart = 644
	configuratorSigningStateEnd   = 1_220
	configuratorEnvelopeSize      = 468
	configuratorSignatureSize     = 501
)

// Sign is concurrent-safe; Close must be serialized against Sign.
type SigningSession struct {
	envelope []byte
	block    cipher.Block
}

func NewConfiguratorSession(state ConfiguratorRequestState, reply []byte) (*SigningSession, error) {
	plain, footer, err := decryptConfiguratorReply(state, reply)
	if err != nil {
		return nil, err
	}
	defer clear(plain)
	envelope, err := configuratorEnvelope(state, plain, footer)
	if err != nil {
		return nil, err
	}
	return newSigningSession(envelope, plain)
}

func newSigningSession(envelope, plain []byte) (*SigningSession, error) {
	if len(plain) < configuratorSigningStateEnd {
		clear(envelope)
		return nil, errors.New("sapkey: truncated signing state")
	}
	key := sessionKey(plain[configuratorSigningStateStart:configuratorSigningStateEnd])
	defer clear(key[:])
	block, _ := aes.NewCipher(key[:])
	return &SigningSession{envelope: envelope, block: block}, nil
}

func (s *SigningSession) Sign(nonce [16]byte, body []byte) []byte {
	out := make([]byte, configuratorSignatureSize)
	out[0] = 2
	copy(out[1:17], nonce[:])
	copy(out[33:], s.envelope)
	writeTag(out[17:33], s.block, nonce[:], s.envelope, body)
	return out
}

func (s *SigningSession) Close() {
	if s != nil {
		clear(s.envelope)
		s.envelope = nil
		s.block = nil
	}
}

func writeTag(out []byte, block cipher.Block, nonce, envelope, body []byte) {
	h := sha1.New()
	h.Write(envelope[4:])
	h.Write(body)
	var digest [sha1.Size]byte
	h.Sum(digest[:0])
	subtle.XORBytes(out, nonce, digest[:16])
	block.Decrypt(out, out)
	// SHA-1's remaining four bytes mask only the start of the second block.
	subtle.XORBytes(out[:4], out[:4], digest[aes.BlockSize:])
	block.Decrypt(out, out)
}

func configuratorEnvelope(state ConfiguratorRequestState, plaintext, footer []byte) ([]byte, error) {
	remainder := plaintext
	var field []byte
	for index := 0; index < 3; index++ {
		if len(remainder) < 4 {
			return nil, errors.New("sapkey: truncated Configurator response field")
		}
		size := uint64(binary.BigEndian.Uint32(remainder[:4]))
		if size != 16 || size > uint64(len(remainder)-4) {
			return nil, errors.New("sapkey: invalid Configurator response field")
		}
		field, remainder = remainder[4:20], remainder[20:]
	}

	random := state.envelopeRandom
	block, err := aes.NewCipher(field)
	if err != nil {
		return nil, err
	}
	block.Encrypt(random[:16], random[:16])
	variableSize := 16 + int(random[32]%17)
	if len(footer) > configuratorEnvelopeSize-277-variableSize {
		return nil, errors.New("sapkey: Configurator response footer is too large")
	}
	out := make([]byte, configuratorEnvelopeSize)
	binary.BigEndian.PutUint32(out[:4], uint32(len(out)-4))
	out[4] = 3
	binary.BigEndian.PutUint32(out[5:9], 1)
	binary.BigEndian.PutUint32(out[9:13], uint32(len(state.credential)))
	copy(out[13:269], state.credential[:])
	binary.BigEndian.PutUint32(out[269:273], uint32(variableSize))
	copy(out[273:273+variableSize], random[:variableSize])
	binary.BigEndian.PutUint32(out[273+variableSize:277+variableSize], uint32(len(footer)))
	copy(out[277+variableSize:], footer)
	return out, nil
}

func decryptConfiguratorReply(state ConfiguratorRequestState, reply []byte) (plain, footer []byte, err error) {
	if len(reply) < 21 {
		return nil, nil, errors.New("sapkey: truncated reply")
	}
	size := uint64(binary.BigEndian.Uint32(reply[17:21]))
	if size < 60 || size%aes.BlockSize != 0 || size > uint64(len(reply)-21) {
		return nil, nil, errors.New("sapkey: invalid reply payload length")
	}
	block, _ := aes.NewCipher(state.material[:16])
	plain = make([]byte, int(size))
	cipher.NewCBCDecrypter(block, reply[1:17]).CryptBlocks(plain, reply[21:21+int(size)])
	return plain, reply[21+int(size):], nil
}
