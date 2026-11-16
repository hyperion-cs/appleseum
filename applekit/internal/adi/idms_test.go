package adi

import (
	"bytes"
	"encoding/binary"
	"testing"
	"time"

	"github.com/hyperion-cs/appleseum/applekit/internal/testfixture"
)

func TestIdmsStateRoundTripAndFreshHeaders(t *testing.T) {
	d := testfixture.Load(t)
	raw := d.Idms.Record
	state, err := ParseIdms(append([]byte("IDM1"), raw...))
	if err != nil {
		t.Fatal(err)
	}
	saved, err := state.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	restored, err := ParseIdms(saved)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Unix(d.Time, 0)
	mid, otp, err := restored.Headers(at)
	if err != nil {
		t.Fatal(err)
	}
	if len(mid) != 60 || len(otp) != 28 || binary.BigEndian.Uint32(otp) != 5 {
		t.Fatal("invalid type-1 wrapper")
	}
	want := d.Idms.Otp
	if !bytes.Equal(otp, want[:]) {
		t.Fatal("restored IdMS differs from verified primitive")
	}
	_, later, err := restored.Headers(at.Add(time.Minute))
	if err != nil || bytes.Equal(otp, later) {
		t.Fatal("OTP did not advance", err)
	}
	mid[0] ^= 1
	again, _, err := restored.Headers(at)
	if err != nil || bytes.Equal(mid, again) {
		t.Fatal("mutable header escaped into record")
	}
	if _, err := ParseIdms(append([]byte("IDM1"), make([]byte, 560)...)); err == nil {
		t.Fatal("invalid record accepted")
	}
}

func TestIdmsPublicProvisioning(t *testing.T) {
	d := testfixture.Load(t)
	started, err := BeginIdms(d.Uuid, d.Idms.Spim, time.Unix(d.Time, 0))
	if err != nil {
		t.Fatal(err)
	}
	defer started.Abort()
	cpim := started.ClientData()
	if !bytes.Equal(cpim, d.Idms.Cpim) {
		t.Fatal("IdMS CPIM changed")
	}
	cpim[0] ^= 255
	if bytes.Equal(cpim, started.ClientData()) {
		t.Fatal("mutable CPIM alias")
	}
	ended, err := started.Finish(d.Idms.Ptm, d.Idms.TrustKey)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := ended.MarshalBinary()
	if err != nil || !bytes.Equal(raw[4:], d.Idms.Record) {
		t.Fatal("IdMS record changed")
	}
	mid, otp, err := ended.Headers(time.Unix(d.Time, 0))
	if err != nil || !bytes.Equal(mid, d.Idms.Mid) || !bytes.Equal(otp, d.Idms.Otp) {
		t.Fatal("IdMS headers changed")
	}
	if len(started.ClientData()) != 0 {
		t.Fatal("finished session reusable")
	}
	if _, err := started.Finish(d.Idms.Ptm, d.Idms.TrustKey); err == nil {
		t.Fatal("second End accepted")
	}
}

func TestIdmsZeroStateRejectsOperations(t *testing.T) {
	for _, state := range []*Idms{nil, {}} {
		if _, err := state.MarshalBinary(); err == nil {
			t.Fatal("empty state serialized")
		}
		if _, _, err := state.Headers(time.Now()); err == nil {
			t.Fatal("empty state issued OTP")
		}
		if _, _, err := state.Synchronize(make([]byte, 32), time.Now()); err == nil {
			t.Fatal("empty state synchronized")
		}
	}
}
