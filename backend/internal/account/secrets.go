package account

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/gob"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/hyperion-cs/appleseum/applekit"
)

const (
	KeySize = 32
)

const (
	maxSecretsBytes = 8_388_608
)

type Secrets struct {
	Password      string
	PasswordToken string
	Cookies       []applekit.SessionCookie
}

var ErrKey = errors.New("session key does not match")

func encodeSecrets(value Secrets) ([]byte, error) {
	var out bytes.Buffer
	err := gob.NewEncoder(&out).Encode(value)
	return out.Bytes(), err
}

func decodeSecrets(raw []byte, value *Secrets) error {
	if len(raw) > maxSecretsBytes {
		return errors.New("stored session exceeds size limit")
	}
	input := bytes.NewReader(raw)
	decoder := gob.NewDecoder(input)
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("unexpected trailing session data")
	}
	return nil
}

func Cipher(key []byte) (cipher.AEAD, error) {
	if len(key) != KeySize {
		return nil, errors.New("invalid session key")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCMWithRandomNonce(block)
}

func Seal(key []byte, id string, secrets Secrets) ([]byte, error) {
	box, err := Cipher(key)
	if err != nil {
		return nil, err
	}
	raw, err := encodeSecrets(secrets)
	if err != nil {
		return nil, err
	}
	defer clear(raw)
	return box.Seal(nil, nil, raw, []byte(id)), nil
}

func Open(key []byte, id string, sealed []byte) (Secrets, error) {
	var secrets Secrets
	box, err := Cipher(key)
	if err != nil {
		return secrets, err
	}
	raw, err := box.Open(nil, nil, sealed, []byte(id))
	if err != nil {
		return secrets, ErrKey
	}
	defer clear(raw)
	err = decodeSecrets(raw, &secrets)
	return secrets, err
}

func LoadLocalKey(dir string, required bool) ([]byte, error) {
	path := filepath.Join(dir, "local.key")
	raw, err := os.ReadFile(path)
	if err == nil {
		if len(raw) != KeySize {
			clear(raw)
			return nil, errors.New("invalid local.key; saved secrets were not changed")
		}
		if err = os.Chmod(path, 0600); err != nil {
			clear(raw)
			return nil, err
		}
		return raw, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if required {
		return nil, errors.New("local.key is missing; saved secrets were not changed")
	}
	raw = make([]byte, KeySize)
	if _, err = rand.Read(raw); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		clear(raw)
		return nil, err
	}
	_, err = f.Write(raw)
	if err == nil {
		err = f.Sync()
	}
	err = errors.Join(err, f.Close())
	if err != nil {
		clear(raw)
		return nil, fmt.Errorf("write local.key: %w", err)
	}
	return raw, nil
}
