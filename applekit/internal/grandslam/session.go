package grandslam

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"fmt"
	"strconv"

	"github.com/hyperion-cs/appleseum/applekit/internal/plistutil"
)

// ServiceToken is issued by Apple; it must be passed through without re-encoding.
type ServiceToken struct {
	Token    string `plist:"token"`
	Duration int64  `plist:"duration"`
	Expiry   int64  `plist:"expiry"`
}

// SessionData contains decrypted spd secrets needed by later authentication stages.
type SessionData struct {
	Adsid               string                  `plist:"adsid"`
	DirectoryServicesId string                  `plist:"DsPrsId"`
	Idms                string                  `plist:"GsIdmsToken"`
	Tokens              map[string]ServiceToken `plist:"t"`
}

// PasswordEquivalentToken returns the token verbatim; it may be absent before 2FA.
func (s SessionData) PasswordEquivalentToken() (string, error) {
	token := s.Tokens["com.apple.gs.idms.pet"].Token
	if token == "" {
		return "", errors.New("grandslam: session contains no password-equivalent token")
	}
	return token, nil
}

func decryptSession(key, encrypted []byte) (SessionData, error) {
	if len(encrypted) == 0 || len(encrypted) > 1_048_576 || len(encrypted)%aes.BlockSize != 0 {
		return SessionData{}, errors.New("grandslam: invalid encrypted session length")
	}
	aesKey := deriveKey(key, "extra data key:")
	defer clear(aesKey)
	iv := deriveKey(key, "extra data iv:")
	defer clear(iv)
	block, err := aes.NewCipher(aesKey)
	if err != nil {
		return SessionData{}, err
	}
	plaintext := make([]byte, len(encrypted))
	defer clear(plaintext)
	cipher.NewCBCDecrypter(block, iv[:aes.BlockSize]).CryptBlocks(plaintext, encrypted)
	padding := int(plaintext[len(plaintext)-1])
	if padding < 1 || padding > aes.BlockSize || !bytes.Equal(plaintext[len(plaintext)-padding:], bytes.Repeat([]byte{byte(padding)}, padding)) {
		return SessionData{}, errors.New("grandslam: invalid session padding")
	}
	body := bytes.TrimSpace(plaintext[:len(plaintext)-padding])
	var wire struct {
		Adsid               string                  `plist:"adsid"`
		DirectoryServicesId any                     `plist:"DsPrsId"`
		Idms                string                  `plist:"GsIdmsToken"`
		Tokens              map[string]ServiceToken `plist:"t"`
	}
	if _, err := plistutil.Unmarshal(body, &wire); err != nil {
		return SessionData{}, fmt.Errorf("grandslam: decode session plist: %w", err)
	}
	id, err := directoryId(wire.DirectoryServicesId)
	if err != nil {
		return SessionData{}, err
	}
	result := SessionData{Adsid: wire.Adsid, DirectoryServicesId: id, Idms: wire.Idms, Tokens: wire.Tokens}
	if result.Adsid == "" || result.Idms == "" {
		return SessionData{}, errors.New("grandslam: session lacks identity fields")
	}
	return result, nil
}

func directoryId(value any) (string, error) {
	switch v := value.(type) {
	case nil:
		return "", nil // Identity data may precede completion of 2FA.
	case uint64:
		if v > 0 {
			return strconv.FormatUint(v, 10), nil
		}
	case int64:
		if v > 0 {
			return strconv.FormatInt(v, 10), nil
		}
	case string:
		if id, err := strconv.ParseUint(v, 10, 64); err == nil && id > 0 {
			return v, nil
		}
	}
	return "", errors.New("grandslam: invalid session directory identifier")
}

func deriveKey(key []byte, label string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(label))
	return h.Sum(nil)
}
