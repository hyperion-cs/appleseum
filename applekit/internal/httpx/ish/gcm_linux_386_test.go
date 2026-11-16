//go:build linux && 386 && ishcrypto

package ish

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"os"
	"sync"
	"testing"

	"github.com/hyperion-cs/appleseum/applekit/internal/env"
	"golang.org/x/sys/cpu"
)

func TestIshGcm(t *testing.T) {
	var seed uint32 = 127

	next := func() byte { seed ^= seed << 13; seed ^= seed >> 17; seed ^= seed << 5; return byte(seed) }

	for _, keySize := range []int{16, 24, 32} {
		key := make([]byte, keySize)
		for i := range key {
			key[i] = next()
		}
		block, _ := aes.NewCipher(key)
		g, _ := cipher.NewGCM(block)
		for _, length := range []int{0, 1, 15, 16, 17, 4_095, 4_096, 4_097, 8_191, 16_384, 16_385, 16_386} {
			for _, a := range []int{0, 5, 13, 16, 17, 32, 33} {
				input := make([]byte, length)
				aad := make([]byte, a)
				nonce := make([]byte, 12)
				for _, p := range [][]byte{input, aad, nonce} {
					for i := range p {
						p[i] = next()
					}
				}
				sealed := g.Seal(nil, nonce, input, aad)
				backing := make([]byte, len(sealed)+1)
				inplace := backing[1:]
				copy(inplace, sealed)
				out, err := g.Open(inplace[:0], nonce, inplace, aad)
				if err != nil || !bytes.Equal(out, input) {
					t.Fatalf("inplace key=%d n=%d aad=%d err=%v", keySize, length, a, err)
				}
				target := make([]byte, length+9)
				copy(target, []byte("prefix123"))
				prefixed, err := g.Open(target[:9], nonce, sealed, aad)
				if err != nil || !bytes.Equal(prefixed[:9], []byte("prefix123")) || !bytes.Equal(prefixed[9:], input) {
					t.Fatal("prefix")
				}
				for _, kind := range []int{0, 1, 2} {
					broken := bytes.Clone(sealed)
					badNonce := bytes.Clone(nonce)
					if kind == 0 {
						broken[len(broken)-1] ^= 1
					} else if kind == 1 && length > 0 {
						broken[length/2] ^= 1
					} else {
						badNonce[0] ^= 1
					}
					out := bytes.Repeat([]byte{0xa5}, length)
					if _, err := g.Open(out[:0], badNonce, broken, aad); err == nil || !bytes.Equal(out, make([]byte, length)) {
						t.Fatal("corruption rejection/clear", keySize, length, a, kind)
					}
				}
			}
		}
	}
	block, _ := aes.NewCipher(make([]byte, 32))
	g, _ := cipher.NewGCM(block)
	input := make([]byte, 16_385)
	for i := range input {
		input[i] = next()
	}
	aad := []byte{23, 3, 3, 64, 17}
	var nonce [12]byte
	binary.BigEndian.PutUint64(nonce[4:], 123)
	sealed := g.Seal(nil, nonce[:], input, aad)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 32; j++ {
				output, err := g.Open(nil, nonce[:], sealed, aad)
				if err != nil || !bytes.Equal(output, input) {
					t.Errorf("concurrent Open: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()
}

func TestIshGhash(t *testing.T) {
	var seed uint32 = 91

	next := func() byte { seed ^= seed << 13; seed ^= seed >> 17; seed ^= seed << 5; return byte(seed) }

	for range 8 {
		var h [16]byte
		for i := range h {
			h[i] = next()
		}
		table := newIshGhash(&h, 64)
		for _, a := range []int{0, 1, 5, 15, 16, 17, 31, 32} {
			for _, n := range []int{0, 1, 15, 16, 17, 63, 64, 65, 1_023, 1_024, 1_025, 4_095, 4_096, 4_097, 16_384, 16_385} {
				aad, input := make([]byte, a), make([]byte, n)
				for _, data := range [][]byte{aad, input} {
					for i := range data {
						data[i] = next()
					}
				}
				var lengths, want, got [16]byte
				binary.BigEndian.PutUint64(lengths[:], uint64(a)*8)
				binary.BigEndian.PutUint64(lengths[8:], uint64(n)*8)
				cipher.GhashIsh(&want, &h, aad, input, lengths[:])
				table.sum(&got, aad, input, lengths[:])
				if got != want {
					t.Fatalf("GHASH mismatch aad=%d bytes=%d", a, n)
				}
			}
		}
	}
}

// Tests force installation outside iSH too, so CI exercises the real provider.
var installedBeforeTests bool

func TestMain(m *testing.M) {
	installedBeforeTests = te0[0] != 0
	if !installedBeforeTests {
		installIsh()
	}
	os.Exit(m.Run())
}

func TestIshRuntimeGuard(t *testing.T) {
	if installedBeforeTests != (env.IsIsh() && cpu.X86.HasSSE2) {
		t.Fatal("iSH detection")
	}
	block, _ := aes.NewCipher(make([]byte, 32))
	// These APIs must retain their standard behavior and concrete AES block type.
	for _, makeAEAD := range []func() (cipher.AEAD, error){
		func() (cipher.AEAD, error) { return cipher.NewGCMWithNonceSize(block, 8) },
		func() (cipher.AEAD, error) { return cipher.NewGCMWithNonceSize(block, 16) },
		func() (cipher.AEAD, error) { return cipher.NewGCMWithTagSize(block, 12) },
		func() (cipher.AEAD, error) { return cipher.NewGCMWithRandomNonce(block) },
	} {
		g, err := makeAEAD()
		if err != nil {
			t.Fatal(err)
		}
		nonce := make([]byte, g.NonceSize())
		input := make([]byte, 16_384)
		sealed := g.Seal(nil, nonce, input, nil)
		out, err := g.Open(nil, nonce, sealed, nil)
		if err != nil || !bytes.Equal(out, input) {
			t.Fatal("GCM API compatibility", err)
		}
	}
}

func TestIshOverlap(t *testing.T) {
	b, _ := aes.NewCipher(make([]byte, 32))
	g, _ := cipher.NewGCM(b)
	nonce := make([]byte, 12)
	sealed := g.Seal(nil, nonce, make([]byte, 8_192), nil)

	mustPanic := func(f func()) {
		t.Helper()
		defer func() {
			if recover() == nil {
				t.Error("missing overlap/nonce panic")
			}
		}()
		f()
	}

	mustPanic(func() { g.Open(sealed[1:1], nonce, sealed, nil) })
	mustPanic(func() { g.Open(sealed[:0], nonce, sealed, sealed[:5]) })
	mustPanic(func() { g.Open(nil, nonce[:8], sealed, nil) })
}

func TestIshMultiply(t *testing.T) {
	var seed uint32 = 419

	next := func() byte { seed ^= seed << 13; seed ^= seed >> 17; seed ^= seed << 5; return byte(seed) }

	for sample := range 642 {
		var h, input, want [16]byte
		for i := range h {
			h[i], input[i] = next(), next()
		}
		if sample >= 512 && sample < 640 {
			clear(input[:])
			input[(sample-512)/8] = 1 << ((sample - 512) % 8)
		}
		if sample == 640 {
			clear(h[:])
			clear(input[:])
		}
		if sample == 641 {
			for i := range h {
				h[i], input[i] = 0xff, 0xff
			}
		}
		cipher.GhashIsh(&want, &h, input[:])
		var hw, iw, expected, got [4]uint32
		for i := range 4 {
			hw[i] = binary.BigEndian.Uint32(h[i*4:])
			iw[i] = binary.BigEndian.Uint32(input[i*4:])
			expected[i] = binary.BigEndian.Uint32(want[i*4:])
		}
		original := iw
		multiplyIsh(&got, &hw, &iw)
		if got != expected {
			t.Fatal("field multiplication", sample)
		}
		multiplyIsh(&iw, &hw, &iw)
		if iw != expected {
			t.Fatal("input alias", sample)
		}
		multiplyIsh(&hw, &hw, &original)
		if hw != expected {
			t.Fatal("key alias", sample)
		}
	}
}
