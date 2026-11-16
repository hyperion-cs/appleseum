package adi

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
	"time"

	"github.com/hyperion-cs/appleseum/applekit/internal/testfixture"
)

func syntheticAmdRecord(t *testing.T) [amdRecordSize]byte {
	t.Helper()
	return [amdRecordSize]byte(testfixture.Load(t).Amd.Record)
}

func TestAmdStateHeadersRoundTripAndErase(t *testing.T) {
	var hostUuid [16]byte
	copy(hostUuid[:], []byte("synthetic-host-1"))
	state, err := NewAmd(hostUuid)
	if err != nil {
		t.Fatal(err)
	}
	raw := syntheticAmdRecord(t)
	state.record, err = parseOtpRecord(raw[:])
	if err != nil {
		t.Fatal(err)
	}
	at := time.Unix(testfixture.Load(t).Time, 0).UTC()
	headers, err := state.HeadersAmd(at)
	if err != nil {
		t.Fatal(err)
	}
	if len(headers.MachineId) != 60 || len(headers.Otp) != 24 || binary.BigEndian.Uint32(headers.Otp) != 4 || binary.BigEndian.Uint32(headers.Otp[4:]) != 16 {
		t.Fatalf("invalid AMD header shapes: MID=%d OTP=%x", len(headers.MachineId), headers.Otp)
	}
	want := testfixture.Load(t).Amd
	if !bytes.Equal(headers.Otp, want.Otp) || !bytes.Equal(headers.MachineId, want.Mid) {
		t.Fatal("AMD headers differ from frozen outputs")
	}

	encoded, err := state.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	restored, err := ParseAmd(encoded)
	if err != nil {
		t.Fatal(err)
	}
	got, err := restored.HeadersAmd(at)
	if err != nil || !bytes.Equal(got.Otp, headers.Otp) || !bytes.Equal(got.MachineId, headers.MachineId) {
		t.Fatal("AMD state round trip changed headers")
	}
	if err := restored.EraseAmd(); err != nil {
		t.Fatal(err)
	}
	if _, err := restored.HeadersAmd(at); !errors.Is(err, ErrAmdNotProvisioned) {
		t.Fatalf("erased state remained provisioned: %v", err)
	}
	erased, err := restored.MarshalBinary()
	if err != nil || len(erased) != 21 || erased[20] != 0 {
		t.Fatalf("invalid erased state: len=%d err=%v", len(erased), err)
	}
}

func TestAmdProvisioningCommitsExactRecord(t *testing.T) {
	d := testfixture.Load(t)
	p := d.Amd
	state, err := NewAmd(d.Uuid)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Unix(d.Time, 0)
	pending, err := state.BeginAmd(p.Spim, at)
	if err != nil {
		t.Fatal(err)
	}
	cpim := pending.ClientData()
	if !bytes.Equal(cpim, p.Cpim) {
		t.Fatal("AMD CPIM changed")
	}
	cpim[0] ^= 255
	if bytes.Equal(cpim, pending.ClientData()) {
		t.Fatal("mutable CPIM alias")
	}
	if _, err := state.HeadersAmd(at); !errors.Is(err, ErrAmdNotProvisioned) {
		t.Fatal("Start committed before End")
	}
	if err := pending.Finish(p.Ptm, p.TrustKey); err != nil {
		t.Fatal(err)
	}
	pending.Abort()
	encoded, err := state.MarshalBinary()
	if err != nil || !bytes.Equal(encoded[21:], p.Record) {
		t.Fatal("AMD record changed")
	}
	headers, err := state.HeadersAmd(at)
	if err != nil || !bytes.Equal(headers.Otp, p.Otp) || !bytes.Equal(headers.MachineId, p.Mid) {
		t.Fatal("AMD headers changed")
	}
	restored, err := ParseAmd(encoded)
	if err != nil {
		t.Fatal(err)
	}
	got, err := restored.HeadersAmd(at)
	if err != nil || !bytes.Equal(got.Otp, p.Otp) || !bytes.Equal(got.MachineId, p.Mid) {
		t.Fatal("restored AMD headers changed")
	}
	if len(pending.ClientData()) != 0 || !errors.Is(pending.Finish(nil, nil), errProvisioningClosed) {
		t.Fatal("completed session reusable")
	}
}

func TestAmdProvisioningCannotCommitAfterAbortEraseOrNewStart(t *testing.T) {
	for _, operation := range []string{"abort", "erase", "supersede", "bad-finish"} {
		t.Run(operation, func(t *testing.T) {
			d := testfixture.Load(t)
			hostUuid := d.Uuid
			state, _ := NewAmd(hostUuid)
			at := time.Unix(d.Time, 0)
			spim := d.Amd.Spim
			pending, err := state.BeginAmd(spim, at)
			if err != nil {
				t.Fatal(err)
			}
			switch operation {
			case "abort":
				pending.Abort()
			case "erase":
				if err := state.EraseAmd(); err != nil {
					t.Fatal(err)
				}
			case "supersede":
				newer, err := state.BeginAmd(spim, at)
				if err != nil {
					t.Fatal(err)
				}
				defer newer.Abort()
			case "bad-finish":
				if err := pending.Finish(nil, nil); err == nil {
					t.Fatal("invalid End succeeded")
				}
			}
			if err := pending.Finish(d.Amd.Ptm, d.Amd.TrustKey); !errors.Is(err, errProvisioningClosed) {
				t.Fatalf("stale session End: %v", err)
			}
			if _, err := state.HeadersAmd(at); !errors.Is(err, ErrAmdNotProvisioned) {
				t.Fatal("stale End resurrected record")
			}
		})
	}
}

func TestAmdStateRejectsForeignRecords(t *testing.T) {
	var hostUuid [16]byte
	copy(hostUuid[:], []byte("synthetic-host-1"))
	raw := syntheticAmdRecord(t)
	binary.BigEndian.PutUint64(raw[20:], ^uint64(1)) // account -2, the IdMS context.
	encoded := append(append([]byte(amdStateVersion), hostUuid[:]...), 1)
	encoded = append(encoded, raw[:]...)
	if _, err := ParseAmd(encoded); err == nil {
		t.Fatal("IdMS record accepted as AMD state")
	}
}
