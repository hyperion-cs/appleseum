package account

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"math/big"
)

const (
	TokenSize = 32
)

func RandomToken() string {
	raw := make([]byte, TokenSize)
	rand.Read(raw)
	return base64.RawURLEncoding.EncodeToString(raw)
}

func RandomPassword() string {
	const (
		alphabet       = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
		passwordLength = 12
	)
	password := make([]byte, passwordLength)
	for i := range password {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			panic(err)
		}
		password[i] = alphabet[n.Int64()]
	}
	return string(password)
}

func TokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
