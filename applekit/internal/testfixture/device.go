// Package testfixture provides independent synthetic data for offline tests.
package testfixture

import (
	_ "embed"
	"encoding/json"
	"testing"
)

type Provision struct {
	Spim, Cpim, Ptm, TrustKey, Record, Mid, Otp, Sim, Srm []byte
}

type Device struct {
	Source, Id, Guid, LocalUser string
	Uuid                        [16]byte
	Time                        int64
	Idms, Amd, AmdLong          Provision
}

//go:embed testdata/device.json
var encoded []byte

// Load returns an independent copy so tests can mutate it without sharing state.
func Load(t testing.TB) Device {
	t.Helper()
	var device Device
	if err := json.Unmarshal(encoded, &device); err != nil {
		t.Fatal(err)
	}
	return device
}

type Keybag struct {
	Hardware []byte
	Dsid     uint64
	Blob     []byte
	Padding  int
}

//go:embed testdata/kbsync.json
var keybags []byte

func Keybags(t testing.TB) []Keybag {
	t.Helper()
	var cases []Keybag
	if err := json.Unmarshal(keybags, &cases); err != nil {
		t.Fatal(err)
	}
	return cases
}
