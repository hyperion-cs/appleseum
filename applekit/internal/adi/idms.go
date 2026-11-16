package adi

import (
	"errors"
	"time"
)

// IdMS routing is persisted separately by the installation owner.
type Idms otpRecord

var errNilIdms = errors.New("adi: nil IdMS state")

func ParseIdms(data []byte) (*Idms, error) {
	if len(data) != 4+softwareRecordSize || string(data[:4]) != "IDM1" {
		return nil, errors.New("adi: invalid IdMS state")
	}
	record, err := parseOtpRecord(data[4:])
	if err != nil || record.otp.header != 5 {
		return nil, errors.New("adi: invalid IdMS record")
	}
	return (*Idms)(record), nil
}

func (s *Idms) MarshalBinary() ([]byte, error) {
	if s == nil || s.otp.block == nil {
		return nil, errNilIdms
	}
	return append([]byte("IDM1"), s.raw...), nil
}

func (s *Idms) Headers(at time.Time) (machineId, otp []byte, err error) {
	if s == nil || s.otp.block == nil {
		return nil, nil, errNilIdms
	}
	return (*otpRecord)(s).headers(at)
}

func (s *Idms) Synchronize(sim []byte, at time.Time) (machineId, clientData []byte, err error) {
	if s == nil || s.otp.block == nil {
		return nil, nil, errNilIdms
	}
	return (*otpRecord)(s).synchronize(sim, at)
}

type IdmsProvisioning struct {
	provisioningExchange
}

func BeginIdms(hostUuid [16]byte, spim []byte, at time.Time) (*IdmsProvisioning, error) {
	cpim, session, err := executeProvisioningStart(-2, spim, hostUuid, at)
	if err != nil {
		return nil, err
	}
	return &IdmsProvisioning{provisioningExchange{cpim: cpim, session: session}}, nil
}

func (s *IdmsProvisioning) ClientData() []byte {
	if s == nil {
		return nil
	}
	return s.provisioningExchange.ClientData()
}

func (s *IdmsProvisioning) Abort() {
	if s != nil {
		s.provisioningExchange.Abort()
	}
}

func (s *IdmsProvisioning) Finish(ptm, transportKey []byte) (*Idms, error) {
	if s == nil {
		return nil, errProvisioningClosed
	}
	record, err := s.provisioningExchange.finish(ptm, transportKey, nil)
	return (*Idms)(record), err
}
