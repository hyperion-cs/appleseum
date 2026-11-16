package adi

import (
	"bytes"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/hyperion-cs/appleseum/applekit/internal/testfixture"
)

func TestPreparedRecordHeadersAreConcurrentAndCoherent(t *testing.T) {
	d := testfixture.Load(t)
	at := time.Unix(d.Time, 0)
	idms, err := ParseIdms(append([]byte("IDM1"), d.Idms.Record...))
	if err != nil {
		t.Fatal(err)
	}
	encoded := append(append([]byte("AMD1"), d.Uuid[:]...), 1)
	encoded = append(encoded, d.Amd.Record...)
	amd, err := ParseAmd(encoded)
	if err != nil {
		t.Fatal(err)
	}
	expected, err := amd.HeadersAmd(at)
	if err != nil {
		t.Fatal(err)
	}
	var idmsExpected [8][]byte
	for i := range idmsExpected {
		_, idmsExpected[i], err = idms.Headers(at.Add(time.Duration(i) * 30 * time.Second))
		if err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			for i := 0; i < 64; i++ {
				_, otp, err := idms.Headers(at.Add(time.Duration(i%8) * 30 * time.Second))
				if err != nil || !bytes.Equal(otp, idmsExpected[i%8]) {
					t.Error("shared IdMS cipher mixed counters")
					return
				}
				headers, err := amd.HeadersAmd(at)
				if errors.Is(err, ErrAmdNotProvisioned) {
					continue
				}
				if err != nil || !bytes.Equal(headers.Otp, expected.Otp) || !bytes.Equal(headers.MachineId, expected.MachineId) {
					t.Error("AMD returned incoherent MID/OTP")
					return
				}
			}
		})
	}
	for range 32 {
		if err := amd.EraseAmd(); err != nil {
			t.Fatal(err)
		}
		restored, err := ParseAmd(encoded)
		if err != nil {
			t.Fatal(err)
		}
		amd.mu.Lock()
		amd.record = restored.record
		amd.mu.Unlock()
	}
	wg.Wait()
	stored, err := amd.MarshalBinary()
	if err != nil || !bytes.Equal(stored, encoded) {
		t.Fatal("prepared state changed serialized record")
	}
}
