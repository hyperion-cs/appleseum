package adi

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"math"
	"time"
)

const (
	softwareRecordSize     = 560
	softwareRecordOverhead = 160 // Header, five field descriptors and all non-key fields.
	amdRecordSize          = 432
	accountRecordTag       = 0x30392033
	accountIdTag           = 0x34203837
	machineIdTag           = 0x20356520
	keyDataTag             = 0x63332035
	streamDataTag          = 0x320a3030
	routingTag             = 0x8f72b01c
)

var errSoftwareRecord = errors.New("adi: invalid or unsupported software record")

func otpTime(at time.Time) bool {
	return !at.IsZero() && at.Unix() >= 0 && at.Unix() <= math.MaxUint32
}

// Single-account record; descriptors may appear in any order.
func parseOtpRecord(data []byte) (*otpRecord, error) {
	if len(data) != softwareRecordSize && len(data) != amdRecordSize {
		return nil, errSoftwareRecord
	}
	if binary.BigEndian.Uint32(data[:]) != 1 ||
		binary.BigEndian.Uint32(data[4:]) != accountRecordTag ||
		binary.BigEndian.Uint32(data[8:]) != uint32(len(data)-12) {
		return nil, errSoftwareRecord
	}

	var accountId int64
	var mid, keyData, streamData []byte
	var seen uint8
	for rest := data[12:]; len(rest) > 0; {
		if len(rest) < 8 {
			return nil, errSoftwareRecord
		}
		tag := binary.BigEndian.Uint32(rest)
		size := uint64(binary.BigEndian.Uint32(rest[4:]))
		rest = rest[8:]
		if size > uint64(len(rest)) {
			return nil, errSoftwareRecord
		}
		field := rest[:int(size)]
		rest = rest[int(size):]
		var bit uint8
		switch {
		case tag == accountIdTag && size == 8:
			bit = 1
			accountId = int64(binary.BigEndian.Uint64(field))
		case tag == machineIdTag && size == 60:
			bit = 2
			mid = field
		case tag == keyDataTag && ((len(data) == softwareRecordSize && size == 400) || (len(data) == amdRecordSize && size == 272)):
			bit = 4
			keyData = field
		case tag == streamDataTag && size == 32:
			bit = 8
			streamData = field
		case tag == routingTag && size == 8:
			bit = 16 // Routing is persisted by the installation owner, not here.
		default:
			return nil, errSoftwareRecord
		}
		if seen&bit != 0 {
			return nil, errSoftwareRecord
		}
		seen |= bit
	}
	if seen != 31 || (len(data) == amdRecordSize && accountId != -1) {
		return nil, errSoftwareRecord
	}
	return newOtpRecord(bytes.Clone(data), accountId, mid, keyData, streamData)
}

// Prepared OTP state is immutable; raw bytes are retained for persistence.
type otpRecord struct {
	raw []byte
	mid [60]byte
	otp otpGenerator
}

func newOtpRecord(raw []byte, accountId int64, mid, keyData, streamData []byte) (*otpRecord, error) {
	var otp otpGenerator
	switch {
	case accountId == -1 && len(keyData) == 272:
		otp = newAmdOtp([272]byte(keyData))
	case accountId == -2 && len(keyData) == 400:
		otp = newIdmsOtp([400]byte(keyData), [32]byte(streamData))
	default:
		return nil, errSoftwareRecord
	}
	return &otpRecord{raw: raw, mid: [60]byte(mid), otp: otp}, nil
}

func (r *otpRecord) headers(at time.Time) (mid, otp []byte, err error) {
	if !otpTime(at) {
		return nil, nil, errors.New("adi: invalid OTP time")
	}
	return bytes.Clone(r.mid[:]), r.otp.generate(uint32(at.Unix() / 30)), nil
}

func (r *otpRecord) synchronize(sim []byte, at time.Time) (mid, clientData []byte, err error) {
	clientData, err = r.otp.synchronize(sim, at, rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	return bytes.Clone(r.mid[:]), clientData, nil
}
