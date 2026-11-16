package sapkey

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

// Fixed signature vectors detect changes independently of key representation.
func TestSignatureReferenceVectors(t *testing.T) {
	var x uint32 = 0x57612345
	h := sha256.New()
	for n := 0; n < 4_096; n++ {
		var state [576]byte
		for i := range state {
			x ^= x << 13
			x ^= x >> 17
			x ^= x << 5
			state[i] = byte(x)
		}
		plain := make([]byte, configuratorSigningStateEnd)
		copy(plain[configuratorSigningStateStart:], state[:])
		session, err := newSigningSession(make([]byte, 468), plain)
		if err != nil {
			t.Fatal(err)
		}
		h.Write(session.Sign([16]byte{byte(n), byte(n >> 8), 3, 4}, []byte("synthetic action")))
		session.Close()
	}
	got := hex.EncodeToString(h.Sum(nil))
	want := "52239658ab192b83ff88503f47ba19b03c841ffb83092ec116c03fc6e5eee3ef"
	if got != want {
		t.Fatalf("signatures differ: got %s, want %s", got, want)
	}
}

func BenchmarkSessionKey(b *testing.B) {
	var state [576]byte
	for i := range state {
		state[i] = byte(i)
	}
	b.ResetTimer()
	for b.Loop() {
		_ = sessionKey(state[:])
	}
}

func BenchmarkConfiguratorCredential(b *testing.B) {
	input := bytes.Repeat([]byte{1}, 78)
	entropy := bytes.NewReader(input)
	b.ReportAllocs()
	for b.Loop() {
		entropy.Reset(input)
		if _, err := buildConfiguratorCredential(entropy); err != nil {
			b.Fatal(err)
		}
	}
}
