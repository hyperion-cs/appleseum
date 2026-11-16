package adi

import (
	"testing"
	"time"

	"github.com/hyperion-cs/appleseum/applekit/internal/mt19937"
	"github.com/hyperion-cs/appleseum/applekit/internal/testfixture"
)

func BenchmarkFixedFormat(b *testing.B) {
	for _, tc := range []struct {
		name string
		run  func() byte
	}{
		{"uuid-mid", func() byte { return provisioningMachineId([16]byte{1, 2, 3, 4, 5})[0] }},
		{"idms-digest", func() byte { var input [55]byte; return idmsDigest(input[:])[0] }},
		{"material-key", func() byte { return deriveSoftwareKey([400]byte{})[0] }},
		{"amd-key", func() byte { return deriveAmdKey([272]byte{})[0] }},
		{"context-key", func() byte { return deriveProvisioningContextKey([240]byte{})[0] }},
		{"stream", func() byte { var input [32]byte; softwareStreamCipher.Decrypt(input[:], input[:]); return input[0] }},
		{"stream-wrap", func() byte { return encryptProvisioningStream([32]byte{})[0] }},
		{"checksum", func() byte { return syncChecksum(make([]byte, 16))[0] }},
	} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				benchmarkFormatByte = tc.run()
			}
		})
	}
}

func BenchmarkProvisioningRandom(b *testing.B) {
	b.ReportAllocs()
	for range b.N {
		benchmarkRandom = provisioningRandomStart
	}
}

var benchmarkFormatByte byte
var benchmarkRandom mt19937.Generator

func BenchmarkSoftwareKeyDerivation(b *testing.B) {
	var material [400]byte
	for i := range material {
		material[i] = byte(i*37 + 11)
	}
	b.ReportAllocs()
	for b.Loop() {
		benchmarkImportedKey = deriveSoftwareKeyClear(material)
	}
}

func BenchmarkProvisioningDraw(b *testing.B) {
	r := provisioningRandomStart
	b.ReportAllocs()
	for b.Loop() {
		benchmarkDraw = r.Next()
	}
}

var benchmarkImportedKey [16]byte
var benchmarkDraw uint32

func BenchmarkProvisioningComplete(b *testing.B) {
	d := testfixture.Load(b)
	at := time.Unix(d.Time, 0)
	b.Run("idms", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			p, err := BeginIdms(d.Uuid, d.Idms.Spim, at)
			if err != nil {
				b.Fatal(err)
			}
			result, err := p.Finish(d.Idms.Ptm, d.Idms.TrustKey)
			if err != nil {
				b.Fatal(err)
			}
			_, _, _ = result.Headers(at)
		}
	})
	b.Run("amd", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			state, _ := NewAmd(d.Uuid)
			p, err := state.BeginAmd(d.Amd.Spim, at)
			if err != nil {
				b.Fatal(err)
			}
			if err := p.Finish(d.Amd.Ptm, d.Amd.TrustKey); err != nil {
				b.Fatal(err)
			}
			_, _ = state.HeadersAmd(at)
		}
	})
}
