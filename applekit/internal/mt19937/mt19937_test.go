package mt19937

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"testing"
)

// Cover in-place twist boundaries and uint32 index wrap.
func TestDrawsReference(t *testing.T) {
	const want = "82ae8953da4189fc31aa34e7efe7cc248eaa97ff3198e8df4deeadc0096bc57d"

	h := sha256.New()
	var x uint32 = 0x6d743139
	for range 32 {
		var g Generator
		for i := range g.State {
			x ^= x << 13
			x ^= x >> 17
			x ^= x << 5
			g.State[i] = x
		}
		for _, index := range []uint32{0, 226, 227, 396, 397, 622, 623, 624, 0xfffffffe, 0xffffffff} {
			g.Index = index
			var words [4]byte
			binary.LittleEndian.PutUint32(words[:], g.Next())
			h.Write(words[:])
		}
	}
	got := fmt.Sprintf("%x", h.Sum(nil))
	if got != want {
		t.Fatalf("MT output fingerprint %s, want %s", got, want)
	}
}
