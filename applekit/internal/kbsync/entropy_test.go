package kbsync

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestGeneratedBodyEntropy(t *testing.T) {
	seen := make(map[[12]byte]bool)
	for range 128 {
		message, nonce := bodyInputs()
		if err := checkTestMessage(message); err != nil {
			t.Fatal(err)
		}
		for _, value := range [][]byte{message[50:98], message[128:], nonce[:]} {
			if bytes.IndexByte(value, 0) >= 0 {
				t.Fatal("generated entropy contains a zero byte")
			}
		}
		metadata := binary.BigEndian.Uint32(message[46:50])
		if metadata == 0 || metadata > 0x80000000 {
			t.Fatalf("metadata outside the native range: %08x", metadata)
		}
		var raw [4]byte
		binary.BigEndian.PutUint32(raw[:], (metadata-1)*2|1)
		if bytes.IndexByte(raw[:], 0) >= 0 {
			t.Fatal("metadata cannot come from four nonzero bytes")
		}
		if seen[nonce] {
			t.Fatal("fresh generation repeated a nonce")
		}
		seen[nonce] = true
	}
}
