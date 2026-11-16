package db

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"strconv"
	"sync"

	"github.com/hyperion-cs/appleseum/applekit"
)

type Device struct {
	mu        sync.Mutex
	conn      *sql.DB
	id        string
	sessionId string
	// with is written in the transaction of the next saves; see SaveWith.
	with     *SessionWrite
	replaces bool
	saved    bool
	baseline *applekit.DeviceRecord
}

func (s *Device) Load() (applekit.DeviceRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := ReadDevice(context.Background(), s.conn, s.id)
	if err == nil {
		copy := cloneDevice(d)
		s.baseline = &copy
	}
	return d, err
}

func ReadDevice(ctx context.Context, q querier, id string) (applekit.DeviceRecord, error) {
	if id == "" {
		return applekit.DeviceRecord{}, os.ErrNotExist
	}
	var d applekit.DeviceRecord
	var uuid []byte
	var routing string
	err := q.QueryRowContext(ctx, `SELECT uuid,local_user_id,model,os_version,language,locale,compliance_id,time_zone,idms_generation,idms,routing,amd,storefront FROM devices WHERE id=?`, id).Scan(&uuid, &d.LocalUser, &d.Settings.Model, &d.Settings.OsVersion, &d.Settings.Language, &d.Settings.Locale, &d.Settings.ComplianceId, &d.Settings.TimeZone, &d.IdmsGeneration, &d.Idms, &routing, &d.Amd, &d.Storefront)
	if errors.Is(err, sql.ErrNoRows) {
		return d, os.ErrNotExist
	}
	if err != nil {
		return d, err
	}
	if len(uuid) != len(d.Uuid) {
		return d, errors.New("invalid stored device UUID")
	}
	copy(d.Uuid[:], uuid)
	d.Routing, err = strconv.ParseUint(routing, 10, 64)
	if err != nil {
		return d, err
	}
	if !applekit.ValidDeviceSettings(d.Settings) {
		return d, errors.New("invalid stored Apple device settings")
	}
	return d, nil
}

func (s *Device) Save(d applekit.DeviceRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	offered := cloneDevice(d)
	id := s.id
	if id == "" {
		id = NewId()
	}
	ctx := context.Background()
	tx, err := s.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if s.baseline != nil {
		current, err := ReadDevice(ctx, tx, id)
		if err != nil {
			return err
		}
		if current.Uuid != s.baseline.Uuid {
			return errors.New("Apple device identity changed during authentication")
		}
		if d.Uuid == s.baseline.Uuid {
			if bytes.Equal(d.Idms, s.baseline.Idms) {
				d.Idms, d.IdmsGeneration, d.Routing = current.Idms, current.IdmsGeneration, current.Routing
			}
			if bytes.Equal(d.Amd, s.baseline.Amd) {
				d.Amd = current.Amd
			}
			if d.Settings == s.baseline.Settings {
				d.Settings = current.Settings
			}
			if d.Storefront == s.baseline.Storefront {
				d.Storefront = current.Storefront
			}
		}
	}
	if err = WriteDevice(ctx, tx, id, d); err != nil {
		return err
	}
	if s.sessionId != "" {
		if _, err = tx.ExecContext(ctx, "UPDATE sessions SET device_id=? WHERE id=? AND (device_id IS NULL OR device_id=?)", id, s.sessionId, id); err != nil {
			return err
		}
	}
	if s.replaces {
		if err = invalidateDeviceSessions(ctx, tx, id); err != nil {
			return err
		}
	}
	if s.with != nil {
		if err = s.with.Apply(ctx, tx); err != nil {
			return err
		}
		s.saved = true
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	s.id = id
	s.baseline = &offered
	return nil
}

func cloneDevice(d applekit.DeviceRecord) applekit.DeviceRecord {
	d.Idms = bytes.Clone(d.Idms)
	d.Amd = bytes.Clone(d.Amd)
	return d
}

func WriteDevice(ctx context.Context, tx *sql.Tx, id string, d applekit.DeviceRecord) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO devices(id,uuid,local_user_id,model,os_version,language,locale,compliance_id,time_zone,idms_generation,idms,routing,amd,storefront)
	 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET
	 uuid=excluded.uuid,local_user_id=excluded.local_user_id,model=excluded.model,os_version=excluded.os_version,
	 language=excluded.language,locale=excluded.locale,compliance_id=excluded.compliance_id,time_zone=excluded.time_zone,idms_generation=excluded.idms_generation,
	 idms=excluded.idms,routing=excluded.routing,amd=excluded.amd,storefront=excluded.storefront`,
		id, d.Uuid[:], d.LocalUser, d.Settings.Model, d.Settings.OsVersion, d.Settings.Language, d.Settings.Locale, d.Settings.ComplianceId, d.Settings.TimeZone, d.IdmsGeneration, d.Idms, strconv.FormatUint(d.Routing, 10), d.Amd, d.Storefront)
	return err
}

// Device returns the Apple device store of a session; an empty id creates the device on first save.
func (d *Db) Device(id, sessionId string) *Device {
	return &Device{conn: d.conn, id: id, sessionId: sessionId}
}

func (s *Device) Id() string { s.mu.Lock(); defer s.mu.Unlock(); return s.id }

func (s *Device) SetId(id string) { s.mu.Lock(); defer s.mu.Unlock(); s.id = id }

// SaveWith makes the next saves also write the session, so a registration
// and the session it belongs to are stored together; nil stops it. It
// reports whether a save wrote the session since the last call.
func (s *Device) SaveWith(write *SessionWrite) (saved bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	saved, s.with, s.saved = s.saved, write, false
	return saved
}

// ReplacingIdentity makes the next saves end every Apple session on this
// device, for a change that gives the device a new identity.
func (s *Device) ReplacingIdentity(on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.replaces = on
}

// FindLoginDevice returns the device most recently used to sign in with this login, or "".
func (d *Db) FindLoginDevice(ctx context.Context, login string) (string, error) {
	id, err := value[string](d.conn.QueryRowContext(ctx, "SELECT d.id FROM devices d JOIN account_logins l ON l.account_id=d.account_id LEFT JOIN sessions s ON s.device_id=d.id WHERE l.login=? GROUP BY d.id ORDER BY max(s.authenticated_at) DESC,d.id DESC LIMIT 1", login))
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return id, err
}

func (d *Db) DeviceKnown(ctx context.Context, id string) (bool, error) {
	return value[bool](d.conn.QueryRowContext(ctx, "SELECT account_id IS NOT NULL FROM devices WHERE id=?", id))
}

func (d *Db) DetachDevice(ctx context.Context, sessionId string) error {
	_, err := d.conn.ExecContext(ctx, "UPDATE sessions SET device_id=NULL WHERE id=? AND secrets IS NULL", sessionId)
	return err
}

type DeviceAccess struct {
	Account, Active string
}

func (d *Db) DeviceAccess(ctx context.Context, sessionId, deviceId string) (DeviceAccess, error) {
	var access DeviceAccess
	var active sql.NullString
	err := d.conn.QueryRowContext(ctx, "SELECT COALESCE(d.account_id,''),s.device_id FROM devices d JOIN sessions s ON s.id=? WHERE d.id=?", sessionId, deviceId).Scan(&access.Account, &active)
	access.Active = active.String
	return access, err
}
