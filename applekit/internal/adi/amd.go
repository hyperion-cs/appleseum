package adi

import (
	"errors"
	"sync"
	"time"
)

const (
	// AmdStateHeaderSize excludes the optional provisioned software record.
	AmdStateHeaderSize = amdFlagsOffset + 1
)

const (
	amdStateVersion = "AMD1"
	amdUuidOffset   = len(amdStateVersion)
	amdFlagsOffset  = amdUuidOffset + 16
)

var (
	ErrAmdNotProvisioned = errors.New("adi: AMD is not provisioned")
	errNilAmd            = errors.New("adi: nil AMD state")
)

type AmdHeaders struct {
	MachineId []byte
	Otp       []byte
}

type AmdProvisioningSession interface {
	ClientData() []byte
	Finish(ptm, transportKey []byte) error
	Abort()
}

// Persist the installation UUID and provisioned AMD record together.
type Amd struct {
	mu       sync.Mutex
	hostUuid [16]byte
	record   *otpRecord
	epoch    uint64
}

func NewAmd(hostUuid [16]byte) (*Amd, error) {
	if hostUuid == [16]byte{} {
		return nil, errors.New("adi: AMD requires a non-zero installation UUID")
	}
	return &Amd{hostUuid: hostUuid}, nil
}

func ParseAmd(data []byte) (*Amd, error) {
	if len(data) != AmdStateHeaderSize && len(data) != AmdStateHeaderSize+amdRecordSize {
		return nil, errors.New("adi: invalid AMD state length")
	}
	if string(data[:amdUuidOffset]) != amdStateVersion || (data[amdFlagsOffset] != 0 && data[amdFlagsOffset] != 1) {
		return nil, errors.New("adi: invalid AMD state")
	}
	var hostUuid [16]byte
	copy(hostUuid[:], data[amdUuidOffset:amdFlagsOffset])
	state, err := NewAmd(hostUuid)
	if err != nil {
		return nil, err
	}
	if data[amdFlagsOffset] == 0 {
		if len(data) != AmdStateHeaderSize {
			return nil, errors.New("adi: unprovisioned AMD state has trailing data")
		}
		return state, nil
	}
	if len(data) != AmdStateHeaderSize+amdRecordSize {
		return nil, errors.New("adi: provisioned AMD state is truncated")
	}
	record, err := parseOtpRecord(data[AmdStateHeaderSize:])
	if err != nil || record.otp.header != 4 {
		return nil, errors.New("adi: invalid AMD software record")
	}
	state.record = record
	return state, nil
}

func (a *Amd) MarshalBinary() ([]byte, error) {
	if a == nil {
		return nil, errNilAmd
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	length := AmdStateHeaderSize
	if a.record != nil {
		length += amdRecordSize
	}
	out := make([]byte, length)
	copy(out, amdStateVersion)
	copy(out[amdUuidOffset:amdFlagsOffset], a.hostUuid[:])
	if a.record != nil {
		out[amdFlagsOffset] = 1
		copy(out[AmdStateHeaderSize:], a.record.raw)
	}
	return out, nil
}

// Only the latest BeginAmd can commit; RP invalidates pending exchanges.
func (a *Amd) BeginAmd(spim []byte, at time.Time) (AmdProvisioningSession, error) {
	if a == nil {
		return nil, errNilAmd
	}
	if !otpTime(at) {
		return nil, errors.New("adi: invalid AMD time")
	}
	a.mu.Lock()
	a.epoch++
	epoch := a.epoch
	a.mu.Unlock()
	cpim, session, err := executeProvisioningStart(-1, spim, a.hostUuid, at)
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	current := a.epoch == epoch
	a.mu.Unlock()
	if !current {
		return nil, errProvisioningClosed
	}
	return &amdProvisioningSession{owner: a, epoch: epoch, provisioningExchange: provisioningExchange{cpim: cpim, session: session}}, nil
}

type amdProvisioningSession struct {
	provisioningExchange
	owner *Amd
	epoch uint64
}

func (s *amdProvisioningSession) Finish(ptm, transportKey []byte) error {
	_, err := s.provisioningExchange.finish(ptm, transportKey, func(record *otpRecord) error {
		s.owner.mu.Lock()
		defer s.owner.mu.Unlock()
		if s.owner.epoch != s.epoch {
			return errProvisioningClosed
		}
		s.owner.record = record
		return nil
	})
	return err
}

// Type-2 OTP uses the 24-byte wrapper: 4, 16, payload.
func (a *Amd) HeadersAmd(at time.Time) (AmdHeaders, error) {
	record, err := a.otpState()
	if err != nil {
		return AmdHeaders{}, err
	}
	mid, otp, err := record.headers(at)
	return AmdHeaders{MachineId: mid, Otp: otp}, err
}

// SynchronizeAmd answers an AMS SM action without changing the provisioned record.
func (a *Amd) SynchronizeAmd(sim []byte) (machineId, clientData []byte, err error) {
	record, err := a.otpState()
	if err != nil {
		return nil, nil, err
	}
	return record.synchronize(sim, time.Now())
}

// EraseAmd handles RP locally.
func (a *Amd) EraseAmd() error {
	if a == nil {
		return errNilAmd
	}
	a.mu.Lock()
	a.epoch++
	if a.record != nil {
		clear(a.record.raw)
		a.record = nil
	}
	a.mu.Unlock()
	return nil
}

// An immutable OTP snapshot lets existing callers finish after erase/reprovision.
func (a *Amd) otpState() (*otpRecord, error) {
	if a == nil {
		return nil, errNilAmd
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.record == nil {
		return nil, ErrAmdNotProvisioned
	}
	return a.record, nil
}
