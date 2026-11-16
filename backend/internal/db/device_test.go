package db

import (
	"context"
	"testing"

	"github.com/hyperion-cs/appleseum/applekit"
)

func TestDeviceStoreKeepsConcurrentRegistrationAndRejectsReplacedIdentity(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	created := s.Device("", "")
	auth, err := applekit.NewAuthenticator(applekit.AuthenticatorOptions{Store: created})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = auth.Device(); err != nil {
		t.Fatal(err)
	}
	one, two := s.Device(created.Id(), ""), s.Device(created.Id(), "")
	a, err := one.Load()
	if err != nil {
		t.Fatal(err)
	}
	b, err := two.Load()
	if err != nil {
		t.Fatal(err)
	}
	b.Idms, b.IdmsGeneration, b.Routing = []byte("synthetic-new-registration"), 9, 123
	b.Amd = []byte("synthetic-new-amd")
	if err = two.Save(b); err != nil {
		t.Fatal(err)
	}
	previousOs := a.Settings.OsVersion
	for _, version := range applekit.MacOsVersions() {
		candidate := a.Settings
		candidate.OsVersion = version.Version
		if candidate.OsVersion != previousOs && applekit.ValidDeviceSettings(candidate) {
			a.Settings = candidate
			break
		}
	}
	if a.Settings.OsVersion == previousOs {
		t.Fatal("test device has no alternate compatible macOS version")
	}
	if err = one.Save(a); err != nil {
		t.Fatal(err)
	}
	stored, err := ReadDevice(ctx, s.conn, one.Id())
	if err != nil || string(stored.Idms) != string(b.Idms) || stored.Routing != b.Routing || string(stored.Amd) != string(b.Amd) || stored.Settings.OsVersion != a.Settings.OsVersion {
		t.Fatal("stale settings clobbered a newer registration", err)
	}
	a.Uuid[15] ^= 1
	a.Idms, a.Amd = nil, nil
	a.IdmsGeneration, a.Routing = 0, 0
	if err = one.Save(a); err != nil {
		t.Fatal(err)
	}
	stored, err = ReadDevice(ctx, s.conn, one.Id())
	if err != nil || stored.Uuid != a.Uuid || len(stored.Idms) != 0 || len(stored.Amd) != 0 {
		t.Fatal("identity reset kept another identity's registration", err)
	}
	if err = two.Save(b); err == nil {
		t.Fatal("stale authentication overwrote a replaced device identity")
	}
}
