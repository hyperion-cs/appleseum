package integration_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/hyperion-cs/appleseum/applekit"

	"appleseum/backend/internal/account"
	server "appleseum/backend/internal/app"
	"appleseum/backend/internal/db"
	"appleseum/backend/internal/wire"
)

type timedApple struct{ fakeApple }

func (*timedApple) SessionTimes() (time.Time, time.Time) {
	return time.Unix(1_700_000_000, 0), time.Unix(1_700_000_060, 0)
}

func (c *timedApple) SessionParameters() applekit.SessionParameters {
	p := c.fakeApple.SessionParameters()
	p.AuthenticatedAt, p.LastSuccessAt = c.SessionTimes()
	return p
}

func adminHarness(t *testing.T, dir string, admin bool) *harness {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "packages"), 0700); err != nil {
		t.Fatal(err)
	}
	app, err := newTestServer(server.Config{DataDir: dir, Admin: admin, Apple: &testApple{signIn: func(context.Context, string, string, string) (account.Session, error) {
		return &timedApple{fakeApple{owner: "owner"}}, nil
	}}})
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{app: app, http: httptest.NewServer(app.Handler()), dist: httptest.NewServer(app.Distribution()), client: &http.Client{Timeout: 5 * time.Second}}
	var state wire.State
	h.request(t, "GET", "/api/state", nil, 200, &state)
	h.csrf = state.Csrf
	if state.Mode.Admin != admin || state.Mode.Debug {
		t.Fatal("admin/debug flags coupled")
	}
	return h
}

func TestAdminDeviceGateAndPersistence(t *testing.T) {
	dir := t.TempDir()
	off := adminHarness(t, dir, false)
	for _, x := range []struct{ method, path string }{{"GET", "/api/admin/device"}, {"PUT", "/api/admin/device"}, {"POST", "/api/admin/device/defaults"}, {"POST", "/api/admin/device/reset"}} {
		off.request(t, x.method, x.path, nil, 404, nil)
	}
	assertNoStoredDevice(t, dir)
	off.close()
	h := adminHarness(t, dir, true)
	defer func() {
		if h != nil {
			h.close()
		}
	}()
	var first, updated wire.DeviceProfile
	h.request(t, "GET", "/api/admin/device", nil, 200, &first)
	if first.Id == "" || first.Settings.Model == "" || first.Settings.OsVersion == "" || first.Settings.Locale != "en-US" || first.Settings.TimeZone != "UTC" {
		t.Fatal("missing initial device settings")
	}
	req, _ := http.NewRequest("POST", h.http.URL+"/api/admin/device/reset", nil)
	resp, err := h.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatal("reset bypassed CSRF")
	}
	d := first.Settings
	d.TimeZone = "Missing/Zone"
	h.request(t, "PUT", "/api/admin/device", d, 400, nil)
	h.request(t, "PUT", "/api/admin/device", map[string]any{"language": "en-us", "locale": "en_US", "timeZone": "UTC", "complianceId": "US", "model": "injected"}, 400, nil)
	d = first.Settings
	d.Model, d.OsVersion = "MacBookAir9,1", "15.7"
	d.Locale = "ru-KZ"
	d.TimeZone = "Asia/Almaty"
	h.request(t, "PUT", "/api/admin/device", d, 200, &updated)
	if updated.Id == first.Id || updated.Guid == first.Guid || updated.Settings != d {
		t.Fatal("model change failed to replace identity")
	}
	rawDevice := storedDeviceProfile(t, dir)
	if rawDevice.Settings != (applekit.DeviceSettings{Model: "MacBookAir9,1", OsVersion: "15.7", Language: "ru-kz", Locale: "ru_KZ", ComplianceId: "KZ", TimeZone: "Asia/Almaty"}) {
		t.Fatal("unified locale did not reach Apple metadata", rawDevice.Settings)
	}
	h.close()
	h = adminHarness(t, dir, true)
	var restored wire.DeviceProfile
	h.request(t, "GET", "/api/admin/device", nil, 200, &restored)
	if !reflect.DeepEqual(restored, updated) {
		t.Fatal("settings lost across restart")
	}
	h.request(t, "POST", "/api/admin/device/defaults", nil, 200, &updated)
	if updated.Id == restored.Id || updated.Guid == restored.Guid || updated.Settings != first.Defaults {
		t.Fatal("restoring a different model failed to replace identity")
	}
	h.request(t, "POST", "/api/admin/device/reset", nil, 200, &updated)
	// The response shows no registration state; the stored device proves the reset.
	if stored := storedDeviceProfile(t, dir); updated.Id == first.Id || updated.Guid == first.Guid || stored.IdmsProvisioned || stored.AmdProvisioned {
		t.Fatal("reset reused old device")
	}
}

func TestUnifiedDeviceLocaleValidationAndMetadata(t *testing.T) {
	dir := t.TempDir()
	h := adminHarness(t, dir, true)
	defer h.close()
	for _, locale := range []string{"en", "und-US", "ru_RU", "en-001", "en-ZZ", "en-US-u-ca-gregory", "xx-US", "en-US\r\nInjected: value"} {
		h.request(t, "PUT", "/api/admin/device", wire.DeviceSettings{Model: "MacBookPro18,1", OsVersion: "26.5", Locale: locale, TimeZone: "UTC"}, 400, nil)
	}
	assertNoStoredDevice(t, dir)
	for _, platform := range []struct{ model, version string }{{"MacBookAir8,2", "15.7"}, {"MacBookAir9,1", "26.5"}, {"Mac17,3", "15.7"}, {"MacBookPro18,1", "26.5\r\nInjected: value"}} {
		h.request(t, "PUT", "/api/admin/device", wire.DeviceSettings{Model: platform.model, OsVersion: platform.version, Locale: "en-US", TimeZone: "UTC"}, 400, nil)
	}
	for _, tc := range []struct {
		locale, canonical, language, appleLocale, region string
	}{
		{"en-gb", "en-GB", "en-gb", "en_GB", "GB"},
		{"sr-Latn-RS", "sr-Latn-RS", "sr-latn-rs", "sr_Latn_RS", "RS"},
		{"kk-KZ", "kk-KZ", "kk-kz", "kk_KZ", "KZ"},
	} {
		var profile wire.DeviceProfile
		h.request(t, "PUT", "/api/admin/device", wire.DeviceSettings{Model: "MacBookPro18,1", OsVersion: "26.5", Locale: tc.locale, TimeZone: "UTC"}, 200, &profile)
		if profile.Settings.Locale != tc.canonical {
			t.Fatal("locale not canonicalized", profile.Settings)
		}
		device := storedDeviceProfile(t, dir)
		want := applekit.DeviceSettings{Model: "MacBookPro18,1", OsVersion: "26.5", Language: tc.language, Locale: tc.appleLocale, ComplianceId: tc.region, TimeZone: "UTC"}
		if device.Settings != want {
			t.Fatal("wrong Apple metadata", device.Settings)
		}
	}
}

func TestDeviceLocaleReadDoesNotRewriteAppleSettings(t *testing.T) {
	dir := t.TempDir()
	seeded := seedDevice(t, dir, applekit.DeviceSettings{Model: "MacBookPro18,1", OsVersion: "26.5", Language: "ru-ru", Locale: "ru_RU", ComplianceId: "KZ", TimeZone: "Asia/Almaty"})
	before := storedDeviceState(t, dir)
	h := adminHarness(t, dir, true)
	defer h.close()
	var profile wire.DeviceProfile
	h.request(t, "GET", "/api/admin/device", nil, 200, &profile)
	if !bytes.Equal(before, storedDeviceState(t, dir)) || profile.Id != seeded.Id || profile.Guid != seeded.Guid {
		t.Fatal("reading the unified locale changed the stored device")
	}
	if profile.Settings != (wire.DeviceSettings{Model: "MacBookPro18,1", OsVersion: "26.5", Locale: "ru-KZ", TimeZone: "Asia/Almaty"}) {
		t.Fatal("language and region not combined", profile.Settings)
	}
}

func TestDeviceChangesIgnoreAnotherSessionsQueue(t *testing.T) {
	dir := t.TempDir()
	h := adminHarness(t, dir, true)
	defer h.close()
	conn, err := sql.Open("sqlite", filepath.Join(dir, "metadata.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, _ = conn.Exec("INSERT OR IGNORE INTO accounts(id) VALUES('other')")
	_, err = conn.Exec("INSERT INTO packages(id,app_id,account_id,name,created_at) VALUES('fixture',1,'other','Fixture','2026-11-03T00:00:00Z')")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec("INSERT INTO downloads(package_id,status) VALUES('fixture','queued')"); err != nil {
		t.Fatal(err)
	}
	h.request(t, "POST", "/api/admin/device/reset", nil, 200, nil)
}

func TestProfileSummaryIsLocalAndScopedToAccount(t *testing.T) {
	dir := t.TempDir()
	h := adminHarness(t, dir, false)
	defer h.close()
	h.request(t, "GET", "/api/profile", nil, 401, nil)
	h.request(t, "POST", "/api/login", wire.LoginRequest{Email: "synthetic@example.test", Password: "private"}, 200, nil)
	conn, err := sql.Open("sqlite", filepath.Join(dir, "metadata.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, err = conn.Exec("INSERT INTO catalog(id,account_id,app_id,name,delisted) VALUES('first','owner',1,'First',1),('second','owner',2,'Second',0),('other','other',3,'Other',1)")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = conn.Exec("INSERT OR IGNORE INTO accounts(id) VALUES('other')")
	_, err = conn.Exec("INSERT INTO packages(id,app_id,name,account_id,bundle,version,size,created_at) VALUES('file1',1,'First','owner','test.first','1',123,'2026-11-03T00:00:00Z'),('file2',3,'Other','other','test.other','1',999,'2026-11-03T00:00:00Z')")
	if err != nil {
		t.Fatal(err)
	}
	var result wire.ProfileSummary
	raw := h.request(t, "GET", "/api/profile", nil, 200, &result)
	if result.CatalogApps != 2 || result.DelistedApps != 1 || result.SignedInAt != "2023-11-14T22:13:20Z" {
		t.Fatal("wrong account summary", result)
	}
	var doc map[string]any
	json.Unmarshal(raw, &doc)
	if len(doc) != 5 {
		t.Fatal("unexpected profile fields")
	}
}

func TestLocalMetadataChangesKeepSessionAndRegistration(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "packages"), 0700); err != nil {
		t.Fatal(err)
	}
	client := &timedApple{fakeApple{owner: "owner"}}
	restores := 0
	var restoredSettings applekit.DeviceSettings
	app, err := newTestServer(server.Config{DataDir: dir, Admin: true,
		Apple: &testApple{signIn: func(context.Context, string, string, string) (account.Session, error) {
			return client, nil
		}, open: func(db.Identity) (account.Session, error) {
			restores++
			restoredSettings = storedDeviceProfile(t, dir).Settings
			return client, nil
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{app: app, http: httptest.NewServer(app.Handler()), client: &http.Client{Timeout: 5 * time.Second}, dist: httptest.NewServer(app.Distribution())}
	defer h.close()
	var state wire.State
	h.request(t, "GET", "/api/state", nil, 200, &state)
	h.csrf = state.Csrf
	h.request(t, "POST", "/api/login", wire.LoginRequest{Email: "synthetic@example.test", Password: "fixture", Remember: true}, 200, nil)
	var before, after wire.DeviceProfile
	h.request(t, "GET", "/api/admin/device", nil, 200, &before)
	settings := before.Settings
	settings.Locale, settings.TimeZone = "ru-KZ", "Asia/Almaty"
	h.request(t, "PUT", "/api/admin/device", settings, 200, &after)
	h.request(t, "GET", "/api/state", nil, 200, &state)
	if after.Id != before.Id || after.Settings != settings || state.Account == nil || credentialCount(t, dir) != 1 {
		t.Fatal("metadata update changed the identity or ended the session")
	}
	if restores != 0 {
		t.Fatal("local metadata update opened Apple secrets")
	}
	h.request(t, "POST", "/api/profile/refresh", nil, 200, nil)
	if restores != 1 || restoredSettings.Locale != "ru_KZ" || restoredSettings.TimeZone != "Asia/Almaty" {
		t.Fatal("next Apple operation did not read the updated metadata")
	}
	stored := storedDeviceState(t, dir)
	conn := deviceDb(t, dir)
	if _, err := conn.Exec("CREATE TRIGGER reject_device BEFORE UPDATE ON devices BEGIN SELECT RAISE(FAIL,'synthetic storage failure'); END"); err != nil {
		t.Fatal(err)
	}
	settings.Locale, settings.TimeZone = "en-GB", "Europe/London"
	h.request(t, "PUT", "/api/admin/device", settings, 500, nil)
	if restores != 1 || !bytes.Equal(stored, storedDeviceState(t, dir)) {
		t.Fatal("failed metadata update was not rolled back")
	}
}

func TestLocalIdentityChangesSignOutAtomically(t *testing.T) {
	for _, action := range []string{"model", "defaults", "reset"} {
		t.Run(action, func(t *testing.T) {
			dir := t.TempDir()
			h := adminHarness(t, dir, true)
			defer h.close()
			settings := wire.DeviceSettings{Model: "MacBookAir9,1", OsVersion: "15.7", Locale: "en-US", TimeZone: "UTC"}
			var before, after wire.DeviceProfile
			h.request(t, "PUT", "/api/admin/device", settings, 200, &before)
			h.request(t, "POST", "/api/login", wire.LoginRequest{Email: "synthetic@example.test", Password: "fixture", Remember: true}, 200, nil)
			conn := deviceDb(t, dir)
			if _, err := conn.Exec("INSERT INTO packages(id,app_id,account_id,name,created_at) VALUES('fixture',1,'owner','Fixture','2026-11-03T00:00:00Z')"); err != nil {
				t.Fatal(err)
			}
			// Keep the fixture queued even if the login wake reaches the worker late.
			if _, err := conn.Exec("CREATE TRIGGER hold_fixture_queue BEFORE UPDATE OF status ON downloads WHEN OLD.package_id='fixture' AND NEW.status='downloading' BEGIN SELECT RAISE(FAIL,'hold synthetic queue'); END"); err != nil {
				t.Fatal(err)
			}
			if _, err := conn.Exec("INSERT INTO downloads(package_id,session_id,status) VALUES('fixture','local','queued')"); err != nil {
				t.Fatal(err)
			}
			method, path := "POST", "/api/admin/device/"+action
			if action == "model" {
				method, path, settings = "PUT", "/api/admin/device", before.Defaults
			}
			stored := storedDeviceState(t, dir)
			if _, err := conn.Exec("CREATE TRIGGER reject_signout BEFORE UPDATE OF secrets ON sessions WHEN NEW.secrets IS NULL BEGIN SELECT RAISE(FAIL,'synthetic storage failure'); END"); err != nil {
				t.Fatal(err)
			}
			h.request(t, method, path, settings, 500, nil)
			var state wire.State
			h.request(t, "GET", "/api/state", nil, 200, &state)
			if state.Account == nil || credentialCount(t, dir) != 1 || !bytes.Equal(stored, storedDeviceState(t, dir)) {
				t.Fatal("failed identity change modified the device or signed out")
			}
			if _, err := conn.Exec("DROP TRIGGER reject_signout"); err != nil {
				t.Fatal(err)
			}
			h.request(t, method, path, settings, 200, &after)
			h.request(t, "GET", "/api/state", nil, 200, &state)
			if after.Id == before.Id || after.Guid == before.Guid || after.ReadOnly || state.Account != nil || credentialCount(t, dir) != 0 {
				t.Fatal("identity change retained the old identity or session")
			}
			var status string
			if err := conn.QueryRow("SELECT status FROM downloads WHERE package_id='fixture'").Scan(&status); err != nil || status != "canceled" {
				t.Fatal("identity change did not cancel the old queue", status, err)
			}
		})
	}
}

func TestSignedInOsChangesKeepSessionAndRollbackOnFailure(t *testing.T) {
	dir := t.TempDir()
	// Apple's language and compliance region may differ from the unified locale.
	seedDevice(t, dir, applekit.DeviceSettings{Model: "MacBookPro18,1", OsVersion: "26.5", Language: "en-gb", Locale: "en_GB", ComplianceId: "US", TimeZone: "UTC"})
	client := &timedApple{fakeApple{owner: "owner"}}
	restoredVersion := ""
	logins := 0

	open := func() *harness {
		app, err := newTestServer(server.Config{DataDir: dir, Admin: true,
			Apple: &testApple{signIn: func(context.Context, string, string, string) (account.Session, error) {
				logins++
				return client, nil
			}, open: func(record db.Identity) (account.Session, error) {
				restoredVersion = storedDeviceProfile(t, dir).Settings.OsVersion
				return client, nil
			}},
		})
		if err != nil {
			t.Fatal(err)
		}
		h := &harness{app: app, http: httptest.NewServer(app.Handler()), dist: httptest.NewServer(app.Distribution()), client: &http.Client{Timeout: 5 * time.Second}}
		var state wire.State
		h.request(t, "GET", "/api/state", nil, 200, &state)
		h.csrf = state.Csrf
		return h
	}

	h := open()
	defer func() { h.close() }()
	var before, after wire.DeviceProfile
	h.request(t, "GET", "/api/admin/device", nil, 200, &before)
	h.request(t, "POST", "/api/login", wire.LoginRequest{Email: "synthetic@example.test", Password: "private", Remember: true}, 200, nil)
	settings := before.Settings
	settings.OsVersion = "27.2"
	h.request(t, "PUT", "/api/admin/device", settings, 200, &after)
	if after.Id != before.Id || after.Guid != before.Guid || after.Settings != settings || restoredVersion != "" || logins != 1 {
		t.Fatal("OS update replaced identity or session")
	}
	device := storedDeviceProfile(t, dir)
	if device.Settings.Language != "en-gb" || device.Settings.Locale != "en_GB" || device.Settings.ComplianceId != "US" {
		t.Fatal("OS update rewrote locale metadata")
	}
	var state wire.State
	h.request(t, "GET", "/api/state", nil, 200, &state)
	if state.Account == nil || state.Auth.State != wire.AuthReady || signedInAt(state) != "2023-11-14T22:13:20Z" {
		t.Fatal("OS update ended the session or changed its sign-in time")
	}
	h.request(t, "POST", "/api/profile/refresh", nil, 200, nil)
	if restoredVersion != "27.2" {
		t.Fatal("next Apple operation did not use the new OS version")
	}
	conn, err := sql.Open("sqlite", filepath.Join(dir, "metadata.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var session []byte

	if err := conn.QueryRow("SELECT secrets FROM sessions WHERE id='local'").Scan(&session); err != nil {
		t.Fatal(err)
	}
	if credentialCount(t, dir) != 1 {
		t.Fatal("OS update removed credentials")
	}
	raw := storedDeviceState(t, dir)
	settings.OsVersion = "15.7"
	// A failed device write must roll back session metadata too.
	if _, err := conn.Exec("CREATE TRIGGER reject_device BEFORE UPDATE ON devices BEGIN SELECT RAISE(FAIL,'synthetic storage failure'); END"); err != nil {
		t.Fatal(err)
	}
	h.request(t, "PUT", "/api/admin/device", settings, 500, nil)
	if _, err := conn.Exec("DROP TRIGGER reject_device"); err != nil {
		t.Fatal(err)
	}
	current := storedDeviceState(t, dir)
	h.request(t, "GET", "/api/admin/device", nil, 200, &after)
	if after.Settings.OsVersion != "27.2" {
		t.Fatal("failed OS update published draft device settings")
	}
	var saved []byte
	if err := conn.QueryRow("SELECT secrets FROM sessions WHERE id='local'").Scan(&saved); err != nil {
		t.Fatal(err)
	}
	if restoredVersion != "27.2" || !bytes.Equal(raw, current) || !bytes.Equal(session, saved) {
		t.Fatal("failed OS update changed device or session")
	}
	h.close()
	h = open()
	h.request(t, "GET", "/api/admin/device", nil, 200, &after)
	h.request(t, "GET", "/api/state", nil, 200, &state)
	if state.Account == nil || after.Id != before.Id || after.Settings.OsVersion != "27.2" || restoredVersion != "27.2" || logins != 1 {
		t.Fatal("OS update lost session on restart")
	}
}
