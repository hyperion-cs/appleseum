package adi

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"testing"
	"time"

	"github.com/hyperion-cs/appleseum/applekit/internal/testfixture"
)

func TestSyntheticProvisioning(t *testing.T) {
	d := testfixture.Load(t)
	for name, p := range map[string]testfixture.Provision{"idms": d.Idms, "amd": d.Amd, "amd-long": d.AmdLong} {
		t.Run(name, func(t *testing.T) {
			env := int64(-1)
			if name == "idms" {
				env = -2
			}
			cpim, session, err := executeProvisioningStart(env, p.Spim, d.Uuid, time.Unix(d.Time, 0))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(cpim, p.Cpim) {
				t.Fatal("CPIM changed")
			}
			plain, err := decodeProvisioningEnd(session, p.Ptm, [16]byte(p.TrustKey))
			if err != nil {
				t.Fatal(err)
			}
			record := session.encodeRecord(plain[4 : 4+len(p.Ptm)-provisioningPtmOverhead])
			clear(plain)
			if !bytes.Equal(record, p.Record) {
				t.Fatal("record changed")
			}
			if _, err := parseOtpRecord(record); (err == nil) != (name != "amd-long") {
				t.Fatal("unexpected record support", err)
			}
		})
	}
}

func TestProvisioningRejectsMalformedMessages(t *testing.T) {
	d := testfixture.Load(t)
	at := time.Unix(d.Time, 0)
	for _, offset := range []int{0, 4, 264, 320, 324} {
		spim := bytes.Clone(d.Idms.Spim)
		spim[offset] ^= 1
		if _, _, err := executeProvisioningStart(-2, spim, d.Uuid, at); err == nil {
			t.Fatalf("accepted SPIM field %d", offset)
		}
	}
	for _, n := range []int{0, 346, 348} {
		spim := make([]byte, n)
		if _, err := BeginIdms(d.Uuid, spim, time.Unix(d.Time, 0)); err == nil {
			t.Fatal("invalid SPIM size")
		}
	}
	for _, at := range []time.Time{{}, time.Unix(-1, 0), time.Unix(1<<32, 0)} {
		if _, err := BeginIdms(d.Uuid, d.Idms.Spim, at); err == nil {
			t.Fatal("invalid time")
		}
	}
	if _, err := BeginIdms([16]byte{}, d.Idms.Spim, time.Unix(d.Time, 0)); err == nil {
		t.Fatal("empty UUID")
	}
	_, session, err := executeProvisioningStart(-2, d.Idms.Spim, d.Uuid, at)
	if err != nil {
		t.Fatal(err)
	}
	trustKey := [16]byte(d.Idms.TrustKey)
	for _, offset := range []int{0, 20, len(d.Idms.Ptm) - 23, len(d.Idms.Ptm) - 31} {
		ptm := bytes.Clone(d.Idms.Ptm)
		ptm[offset] ^= 1
		if _, err := decodeProvisioningEnd(session, ptm, trustKey); err == nil {
			t.Fatal("invalid PTM framing/checksum")
		}
	}
	// Re-encrypt synthetic payload mutations so semantic validation is exercised.
	block, _ := aes.NewCipher(session.contextKey[:])
	var key [16]byte
	block.Encrypt(key[:], d.Idms.TrustKey)
	provisioningContextImportCipher.Encrypt(key[:], key[:])
	block, _ = aes.NewCipher(key[:])
	for _, field := range []int{0, 404, 408} {
		ptm := bytes.Clone(d.Idms.Ptm)
		plain := make([]byte, 448)
		cipher.NewCBCDecrypter(block, ptm[4:20]).CryptBlocks(plain, ptm[24:472])
		plain[field] ^= 1
		cipher.NewCBCEncrypter(block, ptm[4:20]).CryptBlocks(ptm[24:472], plain)
		sum := syncChecksum(ptm[4:476])
		copy(ptm[476:496], sum[:])
		if _, err := decodeProvisioningEnd(session, ptm, trustKey); err == nil {
			t.Fatal("invalid PTM payload")
		}
	}
	for _, delta := range []int64{-91, -90, 90, 91} {
		changed := session
		changed.unixSecond += delta
		_, err := decodeProvisioningEnd(changed, d.Idms.Ptm, trustKey)
		if (err == nil) != (delta >= -90 && delta <= 90) {
			t.Fatalf("time boundary %d: %v", delta, err)
		}
	}
	for _, ptm := range [][]byte{nil, d.Idms.Ptm[:502], append(bytes.Clone(d.Idms.Ptm), 0)} {
		if _, err := decodeProvisioningEnd(session, ptm, trustKey); err == nil {
			t.Fatal("invalid PTM length")
		}
	}
}

func TestSyntheticSync(t *testing.T) {
	d := testfixture.Load(t)
	for name, p := range map[string]testfixture.Provision{"idms": d.Idms, "amd": d.Amd} {
		t.Run(name, func(t *testing.T) {
			record, err := parseOtpRecord(p.Record)
			if err != nil {
				t.Fatal(err)
			}
			got, err := record.otp.synchronize(p.Sim, time.Unix(d.Time, 0), bytes.NewReader(bytes.Repeat([]byte{0x35}, 13)))
			if err != nil || !bytes.Equal(got, p.Srm) {
				t.Fatal("Sync regression", err)
			}
			if _, err := record.otp.synchronize(p.Sim, time.Unix(d.Time, 0), bytes.NewReader(nil)); err == nil {
				t.Fatal("missing entropy accepted")
			}
		})
	}
	valid := bytes.Clone(d.Idms.Sim)
	for _, sim := range [][]byte{nil, valid[:31], append(bytes.Clone(valid), 0), make([]byte, 32)} {
		if _, err := (otpGenerator{}).synchronize(sim, time.Unix(d.Time, 0), nil); err == nil {
			t.Fatal("invalid SIM")
		}
	}
	for _, at := range []time.Time{{}, time.Unix(-1, 0), time.Unix(1<<32, 0)} {
		if _, err := (otpGenerator{}).synchronize(valid, at, nil); err == nil {
			t.Fatal("invalid time")
		}
	}
	if _, err := (otpGenerator{}).synchronize(valid, time.Unix(d.Time, 0), nil); err == nil {
		t.Fatal("unsupported record")
	}
	if _, _, err := (*Idms)(nil).Synchronize(valid, time.Now()); err == nil {
		t.Fatal("nil IdMS")
	}
	if _, _, err := (*Amd)(nil).SynchronizeAmd(valid); err == nil {
		t.Fatal("nil AMD")
	}
	amd, _ := NewAmd(d.Uuid)
	if _, _, err := amd.SynchronizeAmd(valid); err != ErrAmdNotProvisioned {
		t.Fatal("unprovisioned AMD")
	}
}

func TestSoftwareRecordValidation(t *testing.T) {
	d := testfixture.Load(t)
	for _, p := range []testfixture.Provision{d.Idms, d.Amd} {
		for n := range len(p.Record) {
			if _, err := parseOtpRecord(p.Record[:n]); err == nil {
				t.Fatal("truncated record")
			}
		}
		for _, offset := range []int{0, 4, 8, 12, 16, 28, 32, 96, 100, len(p.Record) - 16, len(p.Record) - 12} {
			bad := bytes.Clone(p.Record)
			bad[offset] ^= 255
			if _, err := parseOtpRecord(bad); err == nil {
				t.Fatalf("invalid record field %d", offset)
			}
		}
		bad := bytes.Clone(p.Record)
		binary.BigEndian.PutUint32(bad[len(bad)-16:], machineIdTag)
		if _, err := parseOtpRecord(bad); err == nil {
			t.Fatal("duplicate record field")
		}
	}
}

func TestRecordFieldOrderAndOwnership(t *testing.T) {
	d := testfixture.Load(t)
	for _, p := range []testfixture.Provision{d.Idms, d.Amd} {
		var fields [][]byte
		for rest := p.Record[12:]; len(rest) > 0; {
			size := 8 + int(binary.BigEndian.Uint32(rest[4:]))
			fields = append(fields, rest[:size])
			rest = rest[size:]
		}
		raw := bytes.Clone(p.Record[:12])
		for i := len(fields) - 1; i >= 0; i-- {
			raw = append(raw, fields[i]...)
		}
		expected := bytes.Clone(raw)
		record, err := parseOtpRecord(raw)
		if err != nil {
			t.Fatal(err)
		}
		clear(raw)
		mid, otp, err := record.headers(time.Unix(d.Time, 0))
		if err != nil || !bytes.Equal(mid, p.Mid) || !bytes.Equal(otp, p.Otp) || !bytes.Equal(record.raw, expected) {
			t.Fatal("field order or caller mutation changed committed record", err)
		}
	}
}
