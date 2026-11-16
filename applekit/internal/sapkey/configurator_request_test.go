package sapkey

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"testing"
)

func TestConfiguratorRequestSynthetic(t *testing.T) {
	const want = "e26aafadf29d39e87d922eeb557d8246f2bfb6578ed7ba83e817f2f970c1c101"

	// Fixed synthetic entropy makes the request reproducible.
	entropy := make([]byte, 512)
	for i := range entropy {
		entropy[i] = byte(i%255 + 1)
	}
	request, _, err := buildConfiguratorRequest(1, []byte("test-mac"), bytes.NewReader(entropy))
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprintf("%x", sha256.Sum256(request)); got != want {
		t.Fatal("synthetic request changed")
	}
}

func TestConfiguratorRequestRejectsShortEntropy(t *testing.T) {
	if _, _, err := buildConfiguratorRequest(1, []byte{1}, bytes.NewReader(make([]byte, 35))); err == nil {
		t.Fatal("short entropy was accepted")
	}
}

func decodeFixtureHex(t *testing.T, value string) []byte {
	t.Helper()
	decoded, err := hex.DecodeString(value)
	if err != nil {
		t.Fatal(err)
	}
	return decoded
}
