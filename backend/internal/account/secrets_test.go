package account

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSecretsRequireKeyAndSessionIdentity(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	plain := Secrets{Password: "synthetic-password", PasswordToken: "synthetic-token"}
	first, err := Seal(key, "one", plain)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Seal(key, "one", plain)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(first, second) || bytes.Contains(first, []byte(plain.Password)) || bytes.Contains(first, []byte(plain.PasswordToken)) {
		t.Fatal("nonce reuse or plaintext in the sealed record")
	}
	decoded, err := Open(key, "one", first)
	if err != nil || !reflect.DeepEqual(decoded, plain) {
		t.Fatal("could not reopen credentials", err)
	}
	for _, test := range []struct {
		key  []byte
		id   string
		data []byte
	}{
		{bytes.Repeat([]byte{8}, 32), "one", first},
		{key, "two", first},
		{key, "one", first[:len(first)-1]},
	} {
		if _, err = Open(test.key, test.id, test.data); err == nil {
			t.Fatal("accepted a wrong key, swapped row or damaged record")
		}
	}
	first[len(first)-1] ^= 1
	if _, err = Open(key, "one", first); err == nil {
		t.Fatal("accepted modified ciphertext")
	}
}

func TestLocalKeyIsStableAndNeverReplacesMissingOrDamagedState(t *testing.T) {
	dir := t.TempDir()
	if _, err := LoadLocalKey(dir, true); err == nil {
		t.Fatal("replaced a missing key required by encrypted records")
	}
	key, err := LoadLocalKey(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	again, err := LoadLocalKey(dir, true)
	if err != nil || !bytes.Equal(key, again) {
		t.Fatal("key changed after restart", err)
	}
	path := filepath.Join(dir, "local.key")
	if err = os.WriteFile(path, []byte("damaged"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = LoadLocalKey(dir, false); err == nil {
		t.Fatal("replaced a damaged key")
	}
	raw, _ := os.ReadFile(path)
	if string(raw) != "damaged" {
		t.Fatal("failed validation overwrote the existing key")
	}
}
