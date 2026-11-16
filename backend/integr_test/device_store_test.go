package integration_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/hyperion-cs/appleseum/applekit"

	server "appleseum/backend/internal/app"
	"appleseum/backend/internal/db"
	"appleseum/backend/internal/wire"
)

func deviceDb(t *testing.T, dir string) *sql.DB {
	t.Helper()
	conn, err := sql.Open("sqlite", filepath.Join(dir, "metadata.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

func storedDeviceRecord(t *testing.T, dir string) applekit.DeviceRecord {
	t.Helper()
	var d applekit.DeviceRecord
	var uuid []byte
	var routing string
	if err := deviceDb(t, dir).QueryRow("SELECT uuid,local_user_id,model,os_version,language,locale,compliance_id,time_zone,idms_generation,idms,routing,amd,storefront FROM devices WHERE id=(SELECT device_id FROM sessions WHERE id='local')").Scan(&uuid, &d.LocalUser, &d.Settings.Model, &d.Settings.OsVersion, &d.Settings.Language, &d.Settings.Locale, &d.Settings.ComplianceId, &d.Settings.TimeZone, &d.IdmsGeneration, &d.Idms, &routing, &d.Amd, &d.Storefront); err != nil {
		t.Fatal(err)
	}
	copy(d.Uuid[:], uuid)
	var err error
	d.Routing, err = strconv.ParseUint(routing, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func storedDeviceState(t *testing.T, dir string) []byte {
	t.Helper()
	raw, err := json.Marshal(storedDeviceRecord(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

type seededDevice struct{ record *applekit.DeviceRecord }

func (d *seededDevice) Load() (applekit.DeviceRecord, error) {
	if d.record == nil {
		return applekit.DeviceRecord{}, os.ErrNotExist
	}
	return *d.record, nil
}

func (d *seededDevice) Save(record applekit.DeviceRecord) error { d.record = &record; return nil }

// seedDevice makes a device with exactly these Apple settings the local session's device.
func seedDevice(t *testing.T, dir string, settings applekit.DeviceSettings) applekit.DeviceProfile {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "packages"), 0o700); err != nil {
		t.Fatal(err)
	}
	app, err := newTestServer(server.Config{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	app.Close()
	seed := &seededDevice{}
	auth, err := applekit.NewAuthenticator(applekit.AuthenticatorOptions{Store: seed})
	if err != nil {
		t.Fatal(err)
	}
	profile, err := auth.SetDeviceSettings(settings)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := deviceDb(t, dir).Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err = db.WriteDevice(context.Background(), tx, "seeded-device", *seed.record); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec("UPDATE sessions SET device_id='seeded-device' WHERE id='local'"); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return profile
}

type deviceSnapshot []byte

func (s deviceSnapshot) Load() (applekit.DeviceRecord, error) {
	var d applekit.DeviceRecord
	err := json.Unmarshal(s, &d)
	return d, err
}

func (s deviceSnapshot) Save(applekit.DeviceRecord) error { return errors.New("fixture is read-only") }

func storedDeviceProfile(t *testing.T, dir string) applekit.DeviceProfile {
	t.Helper()
	a, err := applekit.NewAuthenticator(applekit.AuthenticatorOptions{Store: deviceSnapshot(storedDeviceState(t, dir))})
	if err != nil {
		t.Fatal(err)
	}
	profile, err := a.Device()
	if err != nil {
		t.Fatal(err)
	}
	return profile
}

func assertNoStoredDevice(t *testing.T, dir string) {
	t.Helper()
	var count int
	if err := deviceDb(t, dir).QueryRow("SELECT COUNT(*) FROM devices").Scan(&count); err != nil || count != 0 {
		t.Fatal("unexpected device state", count, err)
	}
}

func TestSqliteDeviceIdentityIsUniqueAndDefaultsKeepMatchingModel(t *testing.T) {
	var previous wire.DeviceProfile
	for range 2 {
		dir := t.TempDir()
		h := adminHarness(t, dir, true)
		var first, updated wire.DeviceProfile
		h.request(t, "GET", "/api/admin/device", nil, 200, &first)
		if first.Id == previous.Id || first.Guid == previous.Guid {
			t.Fatal("independent databases shared a device")
		}
		before := storedDeviceState(t, dir)
		h.close()
		h = adminHarness(t, dir, true)
		var restored wire.DeviceProfile
		h.request(t, "GET", "/api/admin/device", nil, 200, &restored)
		if !bytes.Equal(before, storedDeviceState(t, dir)) || restored.Settings != first.Settings || restored.Id != first.Id {
			t.Fatal("initial profile changed after restart")
		}
		settings := first.Defaults
		settings.OsVersion = "27.2"
		var matching wire.DeviceProfile
		h.request(t, "PUT", "/api/admin/device", settings, 200, &matching)
		h.request(t, "POST", "/api/admin/device/defaults", nil, 200, &updated)
		if updated.Id != matching.Id || updated.Guid != matching.Guid || updated.Settings != first.Defaults {
			t.Fatal("OS-only defaults replaced device identity")
		}
		previous = updated
		h.close()
	}
}
