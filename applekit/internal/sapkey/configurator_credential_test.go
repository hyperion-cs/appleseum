package sapkey

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"math/rand/v2"
	"testing"
)

func TestConfiguratorCredentialNativeSyntheticVectors(t *testing.T) {
	// Synthetic vectors with fixed nonzero padding.
	for variant, want := range []string{
		"3054aa415de0491dea48f31ba08de6425551fb65cc88aefb1db7086de8265103",
		"fa982890cd4179105451d211d9dd2dc16e89cce41602b8190fb4317820c78a2c",
	} {
		padding := make([]byte, 78)
		for i := range padding {
			padding[i] = byte(i + 1)
			if variant == 1 {
				padding[i] = byte((i*37+11)%255 + 1)
			}
		}
		got, err := buildConfiguratorCredential(bytes.NewReader(padding))
		if err != nil {
			t.Fatal(err)
		}
		if fmt.Sprintf("%x", sha256.Sum256(got[:])) != want {
			t.Fatalf("native synthetic credential %d mismatch", variant)
		}
	}
}

func TestConfiguratorCredentialRejectsIncompleteEntropy(t *testing.T) {
	for _, padding := range [][]byte{nil, bytes.Repeat([]byte{1}, 77), make([]byte, 78)} {
		if got, err := buildConfiguratorCredential(bytes.NewReader(padding)); err == nil || got != [256]byte{} {
			t.Fatal("incomplete nonzero padding accepted")
		}
	}
}

func TestConfiguratorCredentialReplacesZeroPadding(t *testing.T) {
	padding := bytes.Repeat([]byte{1}, 78)
	padding[0] = 0
	// The first block reads 60 bytes, then rejects the zero and reads replacement.
	entropy := append(append(append([]byte{}, padding[:60]...), 0, 1), padding[60:]...)
	got, err := buildConfiguratorCredential(bytes.NewReader(entropy))
	if err != nil {
		t.Fatal(err)
	}
	want, err := buildConfiguratorCredential(bytes.NewReader(bytes.Repeat([]byte{1}, 78)))
	if err != nil || got != want {
		t.Fatal("zero padding replacement differs")
	}
}

// Complete signature vectors include credentials.
func TestComposedCredentialSignatures(t *testing.T) {
	const want = "17554f5916b41396f3636bd3ec586e20e6fd4b2ea9728c9d3bdfa3f071cb4a1d"

	rng := rand.New(rand.NewPCG(0x42, 0x91))
	hash := sha256.New()
	for n := 0; n < 4_096; n++ {
		padding := make([]byte, 256)
		for i := range padding {
			padding[i] = byte(rng.Uint32())
		}
		credential, err := buildConfiguratorCredential(bytes.NewReader(padding))
		if err != nil {
			t.Fatal(err)
		}
		state, reply := configuratorSigningFixture(t)
		state.credential = credential
		session, err := NewConfiguratorSession(state, reply)
		if err != nil {
			t.Fatal(err)
		}
		hash.Write(session.Sign([16]byte{byte(n), byte(n >> 8), 5, 9}, []byte("synthetic action")))
		session.Close()
	}
	got := fmt.Sprintf("%x", hash.Sum(nil))
	if got != want {
		t.Fatalf("composed signature fingerprint: %s", got)
	}
}
