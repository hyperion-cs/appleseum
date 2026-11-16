package adi

import (
	"bytes"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hyperion-cs/appleseum/applekit/internal/testfixture"
)

func TestIdmsProvisioningConcurrentCompletion(t *testing.T) {
	d := testfixture.Load(t)
	for _, abort := range []bool{false, true} {
		pending, err := BeginIdms(d.Uuid, d.Idms.Spim, time.Unix(d.Time, 0))
		if err != nil {
			t.Fatal(err)
		}
		var completed atomic.Int32
		start := make(chan struct{})
		var wg sync.WaitGroup
		for i := range 24 {
			wg.Go(func() {
				<-start
				switch {
				case i%3 == 0:
					record, err := pending.Finish(d.Idms.Ptm, d.Idms.TrustKey)
					if err == nil {
						completed.Add(1)
						raw, err := record.MarshalBinary()
						if err != nil || !bytes.Equal(raw[4:], d.Idms.Record) {
							t.Error("committed record changed")
						}
					}
				case abort && i%3 == 1:
					pending.Abort()
				default:
					data := pending.ClientData()
					if len(data) != 0 && !bytes.Equal(data, d.Idms.Cpim) {
						t.Error("partly cleared client data")
					}
				}
			})
		}
		close(start)
		wg.Wait()
		if n := completed.Load(); n > 1 || !abort && n != 1 {
			t.Fatalf("completed %d times (abort=%v)", n, abort)
		}
		if len(pending.ClientData()) != 0 {
			t.Fatal("completed continuation retained")
		}
		if _, err := pending.Finish(d.Idms.Ptm, d.Idms.TrustKey); err == nil {
			t.Fatal("completed twice")
		}
	}
}

func TestAmdConcurrentEraseCannotResurrectRecord(t *testing.T) {
	d := testfixture.Load(t)
	at := time.Unix(d.Time, 0)
	for range 32 {
		state, _ := NewAmd(d.Uuid)
		pending, err := state.BeginAmd(d.Amd.Spim, at)
		if err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		wg.Go(func() { _ = pending.Finish(d.Amd.Ptm, d.Amd.TrustKey) })
		wg.Go(func() {
			if err := state.EraseAmd(); err != nil {
				t.Error(err)
			}
		})
		wg.Go(func() { _ = pending.ClientData() })
		wg.Wait()
		if _, err := state.HeadersAmd(at); !errors.Is(err, ErrAmdNotProvisioned) {
			t.Fatal("erased record resurrected", err)
		}
	}
}

func TestIdmsInvalidFinishConsumesContinuation(t *testing.T) {
	d := testfixture.Load(t)
	for _, key := range [][]byte{nil, d.Idms.TrustKey[:15]} {
		pending, err := BeginIdms(d.Uuid, d.Idms.Spim, time.Unix(d.Time, 0))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pending.Finish(d.Idms.Ptm, key); err == nil {
			t.Fatal("invalid transport key accepted")
		}
		if len(pending.ClientData()) != 0 {
			t.Fatal("failed continuation retained")
		}
		if _, err := pending.Finish(d.Idms.Ptm, d.Idms.TrustKey); err == nil {
			t.Fatal("failed continuation reused")
		}
	}
}
