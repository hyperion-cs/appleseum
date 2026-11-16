package applekit

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hyperion-cs/appleseum/applekit/internal/adi"
	"github.com/hyperion-cs/appleseum/applekit/internal/testfixture"
)

func TestDeviceIdentityPersistsAndIsIndependent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "device.json")
	a, err := NewAuthenticator(AuthenticatorOptions{StatePath: path})
	if err != nil {
		t.Fatal(err)
	}
	first, err := a.Device()
	if err != nil {
		t.Fatal(err)
	}
	if a.state.Settings == nil || *a.state.Settings != first.Settings {
		t.Fatal("initial profile was not stored with the identity")
	}
	b, _ := NewAuthenticator(AuthenticatorOptions{StatePath: path})
	restored, err := b.Device()
	if err != nil || first != restored {
		t.Fatal("identity not stable", err)
	}
	c, _ := NewAuthenticator(AuthenticatorOptions{StatePath: filepath.Join(t.TempDir(), "device.json")})
	other, err := c.Device()
	if err != nil || other.Id == first.Id || other.Guid == first.Guid {
		t.Fatal("installations shared identity", err)
	}
	if a.state.Uuid[6]>>4 != 4 || a.state.Uuid[8]>>6 != 2 || first.Guid != strings.ToUpper(hex.EncodeToString(a.state.Uuid[:6])) {
		t.Fatal("UUID/GUID relation changed")
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatal("installation is not private")
	}
}

func TestInitialDeviceProfileUsesRecentCompatibleVersions(t *testing.T) {
	var versions []string
	for _, version := range MacOsVersions() {
		if len(versions) == 0 || versionParts(version.Version)[0] != versionParts(versions[len(versions)-1])[0] {
			versions = append(versions, version.Version)
		}
		if len(versions) == 2 {
			break
		}
	}
	for range 32 {
		input := DefaultDeviceSettings()
		input.Model, input.OsVersion = "", ""
		settings, err := initialDeviceSettings(input)
		if err != nil || settings.validate() != nil || !slices.Contains(versions, settings.OsVersion) {
			t.Fatal("initial profile is outdated or incompatible", settings, err)
		}
		settings.Model, settings.OsVersion = "", ""
		if settings != input {
			t.Fatal("platform selection changed locale or time zone")
		}
	}
}

func TestInitialDeviceProfilePreservesExplicitAndLegacySettings(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		path := filepath.Join(t.TempDir(), "device.json")
		options := AuthenticatorOptions{StatePath: path, Model: "MacBookAir9,1", OsVersion: "15.7"}
		want := DefaultDeviceSettings()
		if legacy {
			old, err := newInstallation()
			if err != nil {
				t.Fatal(err)
			}
			if err := saveInstallation(fileDeviceStore(path), old); err != nil {
				t.Fatal(err)
			}
			options.Model, options.OsVersion = "", ""
		} else {
			want.Model, want.OsVersion = options.Model, options.OsVersion
		}
		before, _ := os.ReadFile(path)
		auth, err := NewAuthenticator(options)
		if err != nil {
			t.Fatal(err)
		}
		profile, err := auth.Device()
		if err != nil || profile.Settings != want {
			t.Fatal("existing or explicit profile was randomized", profile.Settings, err)
		}
		after, _ := os.ReadFile(path)
		if legacy && !bytes.Equal(before, after) {
			t.Fatal("reading a legacy installation rewrote it")
		}
		reset, err := auth.ResetDevice()
		if err != nil || reset.Settings != profile.Settings || reset.Id == profile.Id {
			t.Fatal("reset randomized the selected platform", err)
		}
	}
}

type rejectedInitialDeviceStore struct{ err error }

func (rejectedInitialDeviceStore) Load() (DeviceRecord, error) { return DeviceRecord{}, os.ErrNotExist }

func (s rejectedInitialDeviceStore) Save(DeviceRecord) error { return s.err }

func TestInitialDeviceProfileIsNotPublishedAfterSaveFailure(t *testing.T) {
	failure := errors.New("synthetic save failure")
	auth, err := NewAuthenticator(AuthenticatorOptions{Store: rejectedInitialDeviceStore{failure}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Device(); !errors.Is(err, failure) || auth.state != nil {
		t.Fatal("failed initial save published an installation", err)
	}
}

func TestDeviceSettingsPreserveLegacyProvisioningAndReachSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "device.json")
	fixture := testfixture.Load(t)
	old, err := newInstallation()
	if err != nil {
		t.Fatal(err)
	}
	old.Uuid = fixture.Uuid
	old.Idms = append([]byte("IDM1"), fixture.Idms.Record...)
	old.IdmsGeneration = idmsProvisioningGeneration
	amd, err := adi.NewAmd(old.Uuid)
	if err != nil {
		t.Fatal(err)
	}
	old.Amd, err = amd.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	old.Storefront = "143517"
	old.Routing = 17_106_176
	if err = saveInstallation(fileDeviceStore(path), old); err != nil {
		t.Fatal(err)
	}
	a, _ := NewAuthenticator(AuthenticatorOptions{StatePath: path})
	d := DeviceSettings{Model: DefaultDeviceSettings().Model, OsVersion: "15.7", Language: "ru-ru", Locale: "ru_RU", TimeZone: "Asia/Almaty", ComplianceId: "KZ"}
	profile, err := a.SetDeviceSettings(d)
	if err != nil {
		t.Fatal(err)
	}
	if old.Uuid != a.state.Uuid || old.LocalUser != a.state.LocalUser || !bytes.Equal(old.Idms, a.state.Idms) || !bytes.Equal(old.Amd, a.state.Amd) || a.state.Storefront != old.Storefront || a.state.Routing != old.Routing {
		t.Fatal("settings changed installation or account routing")
	}
	b, _ := NewAuthenticator(AuthenticatorOptions{StatePath: path})
	restored, err := b.Device()
	if err != nil || restored != profile {
		t.Fatal("saved settings lost", err)
	}
	snap, err := b.state.snapshot(b.options, time.Unix(1_800_000_000, 0))
	if err != nil {
		t.Fatal(err)
	}
	if snap.Language != d.Language || snap.Locale != d.Locale || snap.TimeZone != d.TimeZone || snap.ComplianceId != d.ComplianceId || snap.Guid != profile.Guid || snap.Cpd["X-Mme-Device-Id"] != profile.Id {
		t.Fatal("protocol snapshot ignored persisted settings")
	}
	if snap.ClientInfo != "<MacBookPro18,1> <macOS;15.7;24G222> <com.apple.AuthKit/1 (com.apple.configurator.ui/2.21)>" {
		t.Fatal("platform not applied to ClientInfo", snap.ClientInfo)
	}
	raw, _ := json.Marshal(profile)
	for _, secret := range []string{old.LocalUser, base64.StdEncoding.EncodeToString(old.Idms), base64.StdEncoding.EncodeToString(old.Amd)} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("profile exposes provisioning secret")
		}
	}
	defaults, err := b.SetDeviceSettings(DefaultDeviceSettings())
	if err != nil || defaults.Id != profile.Id || defaults.Settings != DefaultDeviceSettings() || !bytes.Equal(old.Idms, b.state.Idms) {
		t.Fatal("defaults reset identity", err)
	}
	d.Model = "MacBookAir9,1"
	changed, err := b.SetDeviceSettings(d)
	if err != nil {
		t.Fatal(err)
	}
	if changed.Id == profile.Id || changed.Guid == profile.Guid || b.state.LocalUser == old.LocalUser || changed.IdmsProvisioned || changed.AmdProvisioned || len(b.state.Idms) != 0 || len(b.state.Amd) != 0 || b.state.IdmsGeneration != 0 || b.state.Storefront != "" || b.state.Routing != 0 {
		t.Fatal("model change retained old identity or provisioning")
	}
	c, _ := NewAuthenticator(AuthenticatorOptions{StatePath: path})
	persisted, err := c.Device()
	if err != nil || persisted != changed {
		t.Fatal("changed model identity not persisted", err)
	}
	legacy := d
	legacy.Model, legacy.OsVersion = "", ""
	preserved, err := c.SetDeviceSettings(legacy)
	if err != nil || preserved != changed {
		t.Fatal("legacy settings reset selected platform", err)
	}
	localUser := b.state.LocalUser
	reset, err := b.ResetDevice()
	if err != nil {
		t.Fatal(err)
	}
	if reset.Id == changed.Id || reset.Guid == changed.Guid || reset.IdmsProvisioned || reset.AmdProvisioned || b.state.LocalUser == localUser || b.state.IdmsGeneration != 0 || b.state.Storefront != "" || b.state.Routing != 0 || len(b.state.Idms) != 0 || len(b.state.Amd) != 0 {
		t.Fatal("reset mixed old and new identities")
	}
	c, _ = NewAuthenticator(AuthenticatorOptions{StatePath: path})
	again, err := c.Device()
	if err != nil || again != reset {
		t.Fatal("reset not persisted", err)
	}
}

func TestInvalidDeviceSettingsNeverChangeState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "device.json")
	a, _ := NewAuthenticator(AuthenticatorOptions{StatePath: path})
	invalid := []DeviceSettings{}
	for field, value := range map[string]string{"language": "en\r\nInjected: value", "locale": "", "timeZone": "Missing/Zone", "complianceId": "us"} {
		d := DefaultDeviceSettings()
		switch field {
		case "language":
			d.Language = value
		case "locale":
			d.Locale = value
		case "timeZone":
			d.TimeZone = value
		case "complianceId":
			d.ComplianceId = value
		}
		invalid = append(invalid, d)
	}
	for _, d := range invalid {
		if _, err := a.SetDeviceSettings(d); err == nil {
			t.Fatal("accepted invalid settings")
		}
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("invalid settings created installation")
	}
	before, _ := a.Device()
	raw, _ := os.ReadFile(path)
	for _, d := range invalid {
		a.SetDeviceSettings(d)
	}
	after, _ := a.Device()
	current, _ := os.ReadFile(path)
	if before != after || !bytes.Equal(raw, current) {
		t.Fatal("invalid settings changed state")
	}
	a.options.Store = fileDeviceStore(t.TempDir()) // Atomic rename over a directory fails.
	d := DefaultDeviceSettings()
	d.Model, d.OsVersion = "MacBookAir9,1", "15.7"
	if _, err := a.SetDeviceSettings(d); err == nil {
		t.Fatal("expected storage failure")
	}
	after, _ = a.Device()
	if before != after {
		t.Fatal("failed save changed memory")
	}
	if _, err := a.ResetDevice(); err == nil {
		t.Fatal("expected reset storage failure")
	}
	after, _ = a.Device()
	if before != after {
		t.Fatal("failed reset changed identity")
	}
}

func TestSessionOsUpdateKeepsAuthenticationAndTransport(t *testing.T) {
	flow := &fakeLoginFlow{t: t, password: "first-password"}
	a := testAuthenticator(t, flow)
	client, err := a.SignIn(context.Background(), "person@example.invalid", "first-password", "")
	if err != nil {
		t.Fatal(err)
	}
	httpClient, account := client.s.Client, client.s.Account
	at, success := client.SessionTimes()
	u, _ := url.Parse("https://buy.itunes.apple.com/")
	httpClient.Jar.SetCookies(u, []*http.Cookie{{Name: "synthetic", Value: "unchanged", Path: "/", Secure: true}})
	before, err := client.SessionSecrets()
	if err != nil {
		t.Fatal(err)
	}
	parameters := client.SessionParameters()
	profile, err := a.Device()
	if err != nil {
		t.Fatal(err)
	}
	settings := profile.Settings
	for _, version := range MacOsVersions() {
		candidate := settings
		candidate.OsVersion = version.Version
		if candidate.OsVersion != settings.OsVersion && candidate.validate() == nil {
			settings = candidate
			break
		}
	}
	if settings.OsVersion == profile.Settings.OsVersion {
		t.Fatal("no alternative compatible OS version")
	}
	if err = client.SetOsVersion(settings.OsVersion); err != nil {
		t.Fatal(err)
	}
	after, err := client.SessionSecrets()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(parameters, client.SessionParameters()) || client.s.Client != httpClient || client.s.Account != account {
		t.Fatal("OS update changed credentials, routing or transport")
	}
	newAt, newSuccess := client.SessionTimes()
	if at != newAt || success != newSuccess {
		t.Fatal("OS update changed session times")
	}
	if _, err = a.SetDeviceSettings(settings); err != nil {
		t.Fatal(err)
	}
	restored, err := a.OpenSession(client.Account(), client.OwnerId(), parameters, after, httpClient)
	if err != nil || restored.s.Platform().Version != settings.OsVersion || restored.s.Account != account || len(restored.s.Client.Jar.Cookies(u)) != 1 || restored.s.Client.Transport != httpClient.Transport {
		t.Fatal("typed session reopening lost settings, authentication or connection pools", err)
	}
	if err = client.SetOsVersion("99.9"); err == nil {
		t.Fatal("accepted unsupported OS")
	}
	unchanged, _ := client.SessionSecrets()
	if !reflect.DeepEqual(after, unchanged) || client.s.Platform().Version != settings.OsVersion {
		t.Fatal("invalid OS changed session")
	}
}

func TestInstallationReusesParsedIdmsWithoutPersistingCache(t *testing.T) {
	raw := testfixture.Load(t).Idms.Record
	s, err := newInstallation()
	if err != nil {
		t.Fatal(err)
	}
	s.Idms = append([]byte("IDM1"), raw...)
	before, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.idmsRecord()
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.idmsRecord()
	if err != nil || first != second {
		t.Fatal("IdMS reparsed instead of reused")
	}
	after, err := json.Marshal(s)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("runtime cache changed persisted installation")
	}
	restored, err := parseInstallation(after)
	if err != nil {
		t.Fatal(err)
	}
	if restored.idms == nil {
		t.Fatal("validated IdMS was discarded during load")
	}
	at := time.Unix(1_793_459_094, 0)
	_, oldOtp, err := first.Headers(at)
	if err != nil {
		t.Fatal(err)
	}
	_, newOtp, err := restored.idms.Headers(at)
	if err != nil || !bytes.Equal(oldOtp, newOtp) {
		t.Fatal("cache changed after restore")
	}
}
