//go:build linux && 386 && ishcrypto

// Package ish accelerates AES-GCM for iSH on iOS/iPadOS.
package ish

import (
	"crypto/cipher"
	"runtime"
	"sync"

	"github.com/hyperion-cs/appleseum/applekit/internal/env"
	"golang.org/x/sys/cpu"
)

var te0, te1, te2, te3 [256]uint32
var sbox0 [256]byte
var workersIsh = sync.OnceValue(func() *ishWorkers { return newIshWorkers(runtime.GOMAXPROCS(0)) })

func installIsh() {
	tables, box := cipher.AesTablesIsh()
	// One shared 4.25 KiB copy avoids pointer loads in every emulated AES round.
	te0, te1, te2, te3, sbox0 = *tables[0], *tables[1], *tables[2], *tables[3], *box
	cipher.RegisterGcmIsh(newOpenIsh)
}

func newOpenIsh(view cipher.AesIsh) func(out, nonce, ciphertext, aad []byte) bool {
	var h [16]byte
	view.Encrypt(h[:], h[:])
	context := &ishGcm{key: newIshAes(view), table: newIshGhash(&h, 64), pool: workersIsh()}
	return context.openIsh
}

func init() {
	if env.IsIsh() && cpu.X86.HasSSE2 {
		installIsh()
	}
}
