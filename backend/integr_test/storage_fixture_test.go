package integration_test

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"database/sql"
	"encoding/gob"
	"os"
	"path/filepath"
	"testing"

	"github.com/hyperion-cs/appleseum/applekit"
)

func localSecrets(t *testing.T, dir string) (result struct {
	Password      string
	PasswordToken string
	Cookies       []applekit.SessionCookie
}) {
	t.Helper()
	var sealed []byte
	err := deviceDb(t, dir).QueryRow("SELECT secrets FROM sessions WHERE id='local'").Scan(&sealed)
	if err == sql.ErrNoRows || err == nil && len(sealed) == 0 {
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	key, err := os.ReadFile(filepath.Join(dir, "local.key"))
	if err != nil {
		t.Fatal(err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	box, err := cipher.NewGCMWithRandomNonce(block)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := box.Open(nil, nil, sealed, []byte("local"))
	if err != nil {
		t.Fatal(err)
	}
	if err = gob.NewDecoder(bytes.NewReader(raw)).Decode(&result); err != nil {
		t.Fatal(err)
	}
	clear(raw)
	clear(key)
	return
}
