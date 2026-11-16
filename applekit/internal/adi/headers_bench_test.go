package adi

import (
	"testing"
	"time"

	"github.com/hyperion-cs/appleseum/applekit/internal/testfixture"
)

func BenchmarkRecordHeaders(b *testing.B) {
	for _, kind := range []string{"IdMS", "AMD"} {
		b.Run(kind, func(b *testing.B) {
			d := testfixture.Load(b)
			raw := d.Idms.Record
			if kind == "AMD" {
				raw = d.Amd.Record
			}
			var headers func(time.Time) error
			if kind == "IdMS" {
				state, err := ParseIdms(append([]byte("IDM1"), raw...))
				if err != nil {
					b.Fatal(err)
				}

				headers = func(at time.Time) error { _, _, err := state.Headers(at); return err }
			} else {
				encoded := append(append([]byte("AMD1"), make([]byte, 16)...), 1)
				encoded[4] = 1
				state, err := ParseAmd(append(encoded, raw...))
				if err != nil {
					b.Fatal(err)
				}

				headers = func(at time.Time) error { _, err := state.HeadersAmd(at); return err }
			}
			at := time.Unix(1_793_459_094, 0)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := headers(at.Add(time.Duration(i) * time.Second)); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
