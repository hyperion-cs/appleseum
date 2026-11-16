package sapkey

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/x509"
	"encoding/binary"
	"errors"
	"io"
)

type ConfiguratorRequestState struct {
	secret         [36]byte
	material       [43]byte
	credential     [256]byte
	envelopeRandom [33]byte
}

// The returned setup state belongs to this exchange and must not be reused.
func BuildConfiguratorRequest(certContainer []byte) ([]byte, ConfiguratorRequestState, error) {
	prefix, serial, err := configuratorCertificateIdentity(certContainer)
	if err != nil {
		return nil, ConfiguratorRequestState{}, err
	}
	return buildConfiguratorRequest(prefix, serial, rand.Reader)
}

func buildConfiguratorRequest(prefix byte, serial []byte, entropy io.Reader) ([]byte, ConfiguratorRequestState, error) {
	var state ConfiguratorRequestState
	if len(serial) > 255 {
		return nil, state, errors.New("sapkey: certificate serial is too long")
	}
	if _, err := io.ReadFull(entropy, state.secret[:]); err != nil {
		return nil, state, err
	}
	credential, err := rsa.EncryptOAEP(sha1.New(), entropy, configuratorPublicKey, state.secret[:], nil)
	if err != nil {
		return nil, state, err
	}

	out := make([]byte, 2+len(serial)+256+16+4+48+sha1.Size)
	out[0], out[1] = prefix, byte(len(serial))
	copy(out[2:], serial)
	offset := 2 + len(serial)
	copy(out[offset:], credential)
	offset += len(credential)
	iv := out[offset : offset+aes.BlockSize]
	if _, err := io.ReadFull(entropy, iv); err != nil {
		return nil, ConfiguratorRequestState{}, err
	}
	offset += aes.BlockSize
	binary.BigEndian.PutUint32(out[offset:], 48)
	offset += 4
	payload := out[offset : offset+48]
	payload[0] = 2
	binary.BigEndian.PutUint32(payload[1:5], 1)
	if _, err := io.ReadFull(entropy, state.material[:]); err != nil {
		return nil, ConfiguratorRequestState{}, err
	}
	copy(payload[5:], state.material[:])
	block, err := aes.NewCipher(state.secret[:16])
	if err != nil {
		return nil, ConfiguratorRequestState{}, err
	}
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(payload, payload)
	mac := hmac.New(sha1.New, state.secret[16:])
	_, _ = mac.Write(payload)
	copy(out[offset+len(payload):], mac.Sum(nil))
	state.credential, err = buildConfiguratorCredential(entropy)
	if err != nil {
		return nil, ConfiguratorRequestState{}, err
	}
	if _, err := io.ReadFull(entropy, state.envelopeRandom[:]); err != nil {
		return nil, ConfiguratorRequestState{}, err
	}
	return out, state, nil
}

func configuratorCertificateIdentity(container []byte) (byte, []byte, error) {
	if len(container) < 10 {
		return 0, nil, errors.New("sapkey: short certificate container")
	}
	first := uint64(binary.BigEndian.Uint32(container[2:6])) + 6
	if first+4 > uint64(len(container)) {
		return 0, nil, errors.New("sapkey: invalid first certificate length")
	}
	start := first + 4
	if start+uint64(binary.BigEndian.Uint32(container[first:start])) != uint64(len(container)) {
		return 0, nil, errors.New("sapkey: invalid second certificate length")
	}
	cert, err := x509.ParseCertificate(container[start:])
	if err != nil {
		return 0, nil, err
	}
	serial := cert.SerialNumber.Bytes()
	if len(serial) > 255 {
		return 0, nil, errors.New("sapkey: certificate serial is too long")
	}
	return container[0], serial, nil
}

// The fixed PKCS#1 public key is distinct from the server-supplied certificate.
var configuratorPublicKey = func() *rsa.PublicKey {
	raw, _ := formatAssets.ReadFile("assets/configurator-rsa.pub.der")
	key, err := x509.ParsePKCS1PublicKey(raw)
	if err != nil || key.Size() != 256 || key.E != 65_537 {
		panic("sapkey: invalid embedded Configurator RSA-2048 public key")
	}
	return key
}()
