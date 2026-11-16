package sapkey

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"testing"
)

func TestFreshActionNonces(t *testing.T) {
	seen := make(map[[16]byte]bool)
	for i := 0; i < 32; i++ {
		nonce, err := NewNonce()
		if err != nil {
			t.Fatal(err)
		}
		if seen[nonce] {
			t.Fatal("action nonce was reused")
		}
		seen[nonce] = true
		for _, value := range nonce {
			if value == 0 {
				t.Fatal("nonce violates recovered nonzero encoding")
			}
		}
	}
}

func TestNonceStateReference(t *testing.T) {
	const want = "a9ffdf68d060df3df1613018bc7514370b571cd19d581dbd8bdee0fb4a7bb856"

	h := sha256.New()
	for _, size := range []int{0, 1, 4, 2_495, 2_496, 2_500} {
		for variant := range 16 {
			state := make([]byte, size)
			for i := range state {
				state[i] = byte(i*37 + variant*19)
			}
			before := bytes.Clone(state)
			nonce := nonceFromState(state)
			if !bytes.Equal(before, state) {
				t.Fatal("nonce modified state")
			}
			h.Write(nonce[:])
		}
	}
	got := fmt.Sprintf("%x", h.Sum(nil))
	if got != want {
		t.Fatalf("nonce fingerprint %s, want %s", got, want)
	}
}

func BenchmarkNonceFromState(b *testing.B) {
	state := make([]byte, 2_496)
	for i := range state {
		state[i] = byte(i*37 + 11)
	}
	b.ReportAllocs()
	for b.Loop() {
		benchmarkNonce = nonceFromState(state)
	}
}

var benchmarkNonce [16]byte
