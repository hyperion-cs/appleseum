package db

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/gob"
	"errors"
	"io"
	"time"

	"github.com/hyperion-cs/appleseum/applekit"

	"appleseum/backend/internal/wire"
)

const (
	maxBagBytes = 8_388_608
)

// Identity is who an Apple session belongs to; it stays readable without the session key.
type Identity struct {
	Owner      string
	Account    applekit.Account
	Parameters applekit.SessionParameters
}

// SessionRecord is a saved Apple session; Sealed holds its encrypted secrets.
type SessionRecord struct {
	Identity
	Sealed []byte
}

// SessionWrite replaces a session's parameters and sealed secrets inside a transaction.
type SessionWrite struct {
	Id         string
	Parameters applekit.SessionParameters
	Sealed     []byte
}

func parseTime(value string) time.Time {
	parsed, _ := time.Parse(time.RFC3339Nano, value)
	return parsed
}

func timeText(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

// ReadSession returns the saved Apple session, or not_found when nobody is signed in.
func (d *Db) ReadSession(ctx context.Context, sessionId string) (SessionRecord, error) {
	var record SessionRecord
	var bag []byte
	var authenticated, lastSuccess string
	err := d.conn.QueryRowContext(ctx, `SELECT d.account_id,a.name,
 COALESCE((SELECT login FROM account_logins WHERE account_id=a.id ORDER BY login LIMIT 1),''),
 s.storefront,s.pod,s.authenticated_at,s.last_success_at,s.bag,s.secrets
 FROM sessions s JOIN devices d ON d.id=s.device_id JOIN accounts a ON a.id=d.account_id
 WHERE s.id=? AND s.secrets IS NOT NULL`, sessionId).Scan(&record.Owner, &record.Account.Name, &record.Account.Email,
		&record.Parameters.Storefront, &record.Parameters.Pod, &authenticated, &lastSuccess, &bag, &record.Sealed)
	if err != nil {
		return record, found(err)
	}
	record.Account.StoreFront = record.Parameters.Storefront
	record.Account.Country = applekit.CountryFromStorefront(record.Parameters.Storefront)
	record.Parameters.AuthenticatedAt, record.Parameters.LastSuccessAt = parseTime(authenticated), parseTime(lastSuccess)
	if len(bag) > 0 {
		err = decodeBag(bag, &record.Parameters.Bag)
	}
	return record, err
}

func encodeBag(bag applekit.ServiceBag) ([]byte, error) {
	var out bytes.Buffer
	err := gob.NewEncoder(&out).Encode(bag)
	return out.Bytes(), err
}

func decodeBag(raw []byte, bag *applekit.ServiceBag) error {
	if len(raw) > maxBagBytes {
		return errors.New("stored service bag exceeds size limit")
	}
	decoder := gob.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(bag); err != nil {
		return err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return errors.New("unexpected trailing service bag data")
	}
	return nil
}

// Apply writes the session inside a transaction that also saves its device.
func (w SessionWrite) Apply(ctx context.Context, tx *sql.Tx) error {
	bag, err := encodeBag(w.Parameters.Bag)
	if err != nil {
		return err
	}
	n, err := changed(ctx, tx, "UPDATE sessions SET authenticated_at=?,last_success_at=?,storefront=?,pod=?,bag=?,secrets=? WHERE id=? AND (scope='local' OR expires_at>?)",
		timeText(w.Parameters.AuthenticatedAt), timeText(w.Parameters.LastSuccessAt), w.Parameters.Storefront, w.Parameters.Pod, bag, w.Sealed, w.Id, time.Now().Unix())
	if err == nil && n != 1 {
		return errors.New("session no longer exists")
	}
	return err
}

// Commit writes the session on its own.
func (w SessionWrite) Commit(ctx context.Context, s *Db) error {
	return s.tx(ctx, func(tx *sql.Tx) error { return w.Apply(ctx, tx) })
}

// SealedSession returns the session's encrypted secrets, or nil when it has none.
func (d *Db) SealedSession(ctx context.Context, id string) ([]byte, error) {
	sealed, err := value[[]byte](d.conn.QueryRowContext(ctx, "SELECT secrets FROM sessions WHERE id=?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return sealed, err
}

func (d *Db) HasLocalSecrets(ctx context.Context) (bool, error) {
	return exists(ctx, d.conn, "SELECT 1 FROM sessions WHERE scope='local' AND secrets IS NOT NULL")
}

// ForgetSecrets ends the Apple session; its device and browser session remain.
func (d *Db) ForgetSecrets(ctx context.Context, id string) error {
	_, err := d.conn.ExecContext(ctx, "UPDATE sessions SET secrets=NULL WHERE id=?", id)
	return err
}

func (d *Db) ExpireSession(ctx context.Context, id string) error {
	_, err := d.conn.ExecContext(ctx, "UPDATE sessions SET expires_at=0 WHERE id=?", id)
	return err
}

func (d *Db) LoginService(ctx context.Context, id string) error {
	_, err := d.conn.ExecContext(ctx, "UPDATE sessions SET secrets=NULL,device_id=NULL WHERE id=? AND scope='public'", id)
	return err
}

func cancelSessionDownloads(ctx context.Context, tx *sql.Tx, where string, args ...any) error {
	_, err := tx.ExecContext(ctx, "UPDATE downloads SET status=?,speed=0,error='' WHERE status IN "+activeStatuses+" AND session_id IN ("+where+")", append([]any{wire.Canceled}, args...)...)
	return err
}

// Logout forgets the session's secrets and cancels its downloads.
func (d *Db) Logout(ctx context.Context, id string) error {
	return d.tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "UPDATE sessions SET secrets=NULL WHERE id=?", id); err != nil {
			return err
		}
		return cancelSessionDownloads(ctx, tx, "?", id)
	})
}

type Login struct {
	Owner, Name, DeviceId, SessionId string
	Logins                           []string
	// A different account takes over the session, so its queue is not carried over.
	CancelDownloads bool
	Session         SessionWrite
}

// InstallLogin binds the account, device and browser session and saves the session secrets.
func (d *Db) InstallLogin(ctx context.Context, input Login) error {
	return d.tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "INSERT INTO accounts(id,name) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name,profile_error='',profile_attempted_at=''", input.Owner, input.Name); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE devices SET account_id=? WHERE id=?", input.Owner, input.DeviceId); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE sessions SET device_id=? WHERE id=?", input.DeviceId, input.SessionId); err != nil {
			return err
		}
		if input.CancelDownloads {
			if err := cancelSessionDownloads(ctx, tx, "?", input.SessionId); err != nil {
				return err
			}
		}
		for _, login := range input.Logins {
			if login == "" {
				continue
			}
			if _, err := tx.ExecContext(ctx, "INSERT INTO account_logins(login,account_id) VALUES(?,?) ON CONFLICT(login) DO UPDATE SET account_id=excluded.account_id", login, input.Owner); err != nil {
				return err
			}
		}
		return input.Session.Apply(ctx, tx)
	})
}

// invalidateDeviceSessions ends every Apple session on a device whose identity changes.
func invalidateDeviceSessions(ctx context.Context, tx *sql.Tx, deviceId string) error {
	if _, err := tx.ExecContext(ctx, "UPDATE sessions SET secrets=NULL WHERE device_id=?", deviceId); err != nil {
		return err
	}
	return cancelSessionDownloads(ctx, tx, "SELECT id FROM sessions WHERE device_id=?", deviceId)
}

func (d *Db) SessionDevice(ctx context.Context, id string) (string, error) {
	return value[string](d.conn.QueryRowContext(ctx, "SELECT device_id FROM sessions WHERE id=? AND secrets IS NOT NULL", id))
}

func (d *Db) EnsureLocalSession(ctx context.Context, csrf string) error {
	_, err := d.conn.ExecContext(ctx, "INSERT OR IGNORE INTO sessions(id,scope,csrf) VALUES('local','local',?)", csrf)
	return err
}

type Workspace struct {
	Csrf   string
	Device string
}

// Workspace reads a browser session that has not expired, or the local session.
func (d *Db) Workspace(ctx context.Context, id string) (Workspace, error) {
	var w Workspace
	var device sql.NullString
	err := d.conn.QueryRowContext(ctx, "SELECT csrf,device_id FROM sessions WHERE id=? AND (scope='local' OR expires_at>?)", id, time.Now().Unix()).Scan(&w.Csrf, &device)
	w.Device = device.String
	return w, err
}

func (d *Db) RefreshBrowserSession(ctx context.Context, id string, now, expires time.Time) (bool, error) {
	n, err := changed(ctx, d.conn, "UPDATE sessions SET expires_at=? WHERE id=? AND scope='public' AND expires_at>?", expires.Unix(), id, now.Unix())
	return n == 1, err
}

func (d *Db) CreateBrowserSession(ctx context.Context, id, csrf string, expires time.Time) error {
	_, err := d.conn.ExecContext(ctx, "INSERT INTO sessions(id,scope,csrf,expires_at) VALUES(?,'public',?,?)", id, csrf, expires.Unix())
	return err
}

func (d *Db) ExpiredSessions(ctx context.Context, now time.Time) ([]string, error) {
	return collect(ctx, d.conn, value[string], "SELECT id FROM sessions WHERE scope='public' AND expires_at>0 AND expires_at<=?", now.Unix())
}

// RevokeSession ends a browser session unless it was renewed after the given moment.
func (d *Db) RevokeSession(ctx context.Context, id string, before time.Time) (bool, error) {
	n, err := changed(ctx, d.conn, "UPDATE sessions SET secrets=NULL,expires_at=0 WHERE id=? AND scope='public' AND expires_at<=?", id, before.Unix())
	return n == 1, err
}

// EnsurePreferences gives a new browser session its defaults: the catalog shows
// only delisted apps, newest purchases first, and the library the user's own files.
func (d *Db) EnsurePreferences(ctx context.Context, id string) error {
	return d.tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO preferences(session_id) VALUES(?)", id); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO table_preferences(session_id,resource,sort,direction,delisted,mine) VALUES
 (?1,'catalog','purchasedAt','desc',1,0),(?1,'library','createdAt','desc',0,1),(?1,'shares','createdAt','desc',0,0)
 ON CONFLICT(session_id,resource) DO NOTHING`, id)
		return err
	})
}
