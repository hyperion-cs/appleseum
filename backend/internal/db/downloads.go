package db

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"time"

	"github.com/hyperion-cs/appleseum/applekit/ipa"

	"appleseum/backend/internal/fault"
	"appleseum/backend/internal/wire"
)

// running are transfers a worker or an upload is processing.
var running = statusList(wire.Downloading, wire.Preparing)

func now() string { return time.Now().UTC().Format(time.RFC3339Nano) }

// EnqueueDownloads queues missing versions, excluding the owner's active jobs.
func (d *Db) EnqueueDownloads(ctx context.Context, scope Scope, sessionId string, appIds []int64, versions map[int64]string) error {
	return d.tx(ctx, func(tx *sql.Tx) error {
		insert, err := tx.PrepareContext(ctx, "INSERT INTO packages(id,app_id,account_id,name,created_at) SELECT ?,app_id,account_id,name,? FROM catalog WHERE account_id=? AND app_id=? AND "+scope.catalogDownloadableVersion("?5"))
		if err != nil {
			return err
		}
		defer insert.Close()
		for _, appId := range appIds {
			id := NewId()
			result, err := insert.ExecContext(ctx, id, now(), scope.Owner, appId, versions[appId])
			if err != nil {
				return err
			}
			if n, err := result.RowsAffected(); err != nil || n == 0 {
				if err != nil {
					return err
				}
				continue
			}
			if _, err = tx.ExecContext(ctx, "INSERT INTO downloads(package_id,session_id,status,version_id) VALUES(?,?,?,?)", id, sessionId, wire.Queued, versions[appId]); err != nil {
				return err
			}
		}
		accountLimit, serverLimit := 512, 512
		if scope.Public {
			accountLimit, serverLimit = 128, 4_096
		}
		var accountCount, serverCount int
		if err := tx.QueryRowContext(ctx, "SELECT count(*),COALESCE(sum(account_id=?),0) FROM transfers WHERE app_id>0 AND status IN "+activeStatuses, scope.Owner).Scan(&serverCount, &accountCount); err != nil {
			return err
		}
		if accountCount > accountLimit || serverCount > serverLimit {
			return fault.QueueFull
		}
		return nil
	})
}

func (d *Db) VersionAvailable(ctx context.Context, owner string, appId int64, versionId string) (bool, error) {
	return exists(ctx, d.conn, "SELECT 1 FROM app_versions WHERE account_id=? AND app_id=? AND version_id=? AND position>=0 AND version!=''", owner, appId, versionId)
}

type Import struct {
	Id, Owner, SessionId, Name string
	AppId, Total               int64
}

// BeginImport records an upload as a running transfer, like a download.
func (d *Db) BeginImport(ctx context.Context, input Import) error {
	return d.tx(ctx, func(tx *sql.Tx) error {
		// An empty owner is the local guest or the public service account.
		if _, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO accounts(id) VALUES(?)", input.Owner); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO packages(id,account_id,app_id,name,created_at) VALUES(?,?,?,?,?)", input.Id, input.Owner, input.AppId, input.Name, now()); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "INSERT INTO downloads(package_id,session_id,status,total) VALUES(?,?,?,?)", input.Id, input.SessionId, wire.Downloading, input.Total)
		return err
	})
}

// Queued reports whether the session has a download waiting.
func (d *Db) Queued(ctx context.Context, sessionId string) (bool, error) {
	return exists(ctx, d.conn, "SELECT 1 FROM downloads WHERE session_id=? AND status=?", sessionId, wire.Queued)
}

type Job struct {
	Id, VersionId string
	AppId         int64
}

// ClaimDownload starts the session's oldest queued download; ok is false
// when none is waiting. Queued apps that became visible in the library
// meanwhile are canceled instead.
func (d *Db) ClaimDownload(ctx context.Context, scope Scope, sessionId string) (job Job, ok bool, err error) {
	err = d.tx(ctx, func(tx *sql.Tx) error {
		saved := "SELECT 1 FROM packages p JOIN ready_packages f ON f.app_id=p.app_id WHERE p.id=downloads.package_id"
		if !scope.shared() {
			saved += " AND f.account_id=p.account_id"
		}
		saved += " AND " + savedVersion("p.account_id", "p.app_id", "downloads.version_id")
		if _, err := tx.ExecContext(ctx, "UPDATE downloads SET status=?,speed=0,error='' WHERE status=? AND session_id=? AND EXISTS("+saved+")", wire.Canceled, wire.Queued, sessionId); err != nil {
			return err
		}
		return tx.QueryRowContext(ctx, `UPDATE downloads SET status=?,speed=0,error=''
 WHERE package_id=(SELECT id FROM transfers WHERE status=? AND session_id=? AND account_id=? ORDER BY id LIMIT 1)
 RETURNING package_id,(SELECT app_id FROM packages WHERE id=package_id),version_id`,
			wire.Downloading, wire.Queued, sessionId, scope.Owner).Scan(&job.Id, &job.AppId, &job.VersionId)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return job, false, nil
	}
	return job, err == nil, err
}

type Progress struct {
	Status          wire.DownloadStatus
	Received, Total int64
	Speed           float64
}

// SaveProgress updates a running transfer; a canceled one keeps its state.
func (d *Db) SaveProgress(ctx context.Context, id string, p Progress) error {
	_, err := d.conn.ExecContext(ctx, "UPDATE downloads SET status=?,received=?,total=?,speed=? WHERE package_id=? AND status IN "+running, p.Status, p.Received, max(p.Total, 0), p.Speed, id)
	return err
}

// CancelDownload cancels a waiting transfer at once and marks a running one
// canceling for whoever processes it. It reports whether the transfer was running.
func (d *Db) CancelDownload(ctx context.Context, scope Scope, id string) (bool, error) {
	var status wire.DownloadStatus
	err := d.tx(ctx, func(tx *sql.Tx) error {
		var owner string
		if err := tx.QueryRowContext(ctx, "SELECT status,account_id FROM transfers WHERE id=?", id).Scan(&status, &owner); err != nil {
			return found(err)
		}
		if !scope.Owns(owner) {
			return fault.NotFound
		}
		if status == wire.Done {
			return fault.ActionUnavailable
		}
		next := wire.Canceled
		if status == wire.Downloading || status == wire.Preparing || status == wire.Canceling {
			next = wire.Canceling
		}
		_, err := tx.ExecContext(ctx, "UPDATE downloads SET status=?,speed=0 WHERE package_id=?", next, id)
		status = next
		return err
	})
	return status == wire.Canceling, err
}

// Canceling reports whether someone asked to stop this running transfer.
func (d *Db) Canceling(ctx context.Context, id string) (bool, error) {
	return exists(ctx, d.conn, "SELECT 1 FROM downloads WHERE package_id=? AND status IN "+statusList(wire.Canceling, wire.Canceled), id)
}

// FinishDownload records how a transfer ended; a canceled one stays canceled.
func (d *Db) FinishDownload(ctx context.Context, id string, status wire.DownloadStatus, code fault.Code) error {
	d.terminalMu.Lock()
	defer d.terminalMu.Unlock()
	end := transferEnd{status: status, code: code}
	err := d.finishDownload(ctx, id, end)
	if err != nil {
		if d.terminal == nil {
			d.terminal = make(map[string]transferEnd)
		}
		d.terminal[id] = end
	} else {
		delete(d.terminal, id)
	}
	return err
}

func (d *Db) finishDownload(ctx context.Context, id string, end transferEnd) error {
	stopped := statusList(wire.Canceling, wire.Canceled)
	_, err := d.conn.ExecContext(ctx, "UPDATE downloads SET speed=0,"+
		"status=CASE WHEN status IN "+stopped+" THEN ? ELSE ? END,"+
		"error=CASE WHEN status IN "+stopped+" THEN '' ELSE ? END WHERE package_id=?", wire.Canceled, end.status, end.code, id)
	return err
}

// RetryDownload queues an interrupted download again for the same session.
func (d *Db) RetryDownload(ctx context.Context, id string) error {
	_, err := d.conn.ExecContext(ctx, "UPDATE downloads SET status=?,received=0,total=0,speed=0,error='' WHERE package_id=? AND status IN "+running, wire.Queued, id)
	return err
}

func (d *Db) HasActiveDownloads(ctx context.Context, scope Scope) (bool, error) {
	if scope.ManagesAll() {
		return exists(ctx, d.conn, "SELECT 1 FROM downloads WHERE status IN "+activeStatuses)
	}
	return exists(ctx, d.conn, "SELECT 1 FROM transfers WHERE account_id=? AND status IN "+activeStatuses, scope.Owner)
}

func (d *Db) SessionTransfersRunning(ctx context.Context, sessionId string) (bool, error) {
	return exists(ctx, d.conn, "SELECT 1 FROM downloads WHERE session_id=? AND status IN "+statusList(wire.Downloading, wire.Preparing, wire.Canceling), sessionId)
}

// PublishDownload moves a finished download into the library unless it was canceled.
func (d *Db) PublishDownload(ctx context.Context, id, partial string, metadata ipa.Metadata, size int64) error {
	return d.publish(ctx, id, partial, size, "UPDATE packages SET bundle=?,version=?,display_version=?,minimum_os=?,size=? WHERE id=? AND size IS NULL",
		metadata.Bundle, metadata.Version, metadata.DisplayVersion, metadata.MinimumOs, size, id)
}

// PublishImport moves an uploaded IPA into the uploader's library unless it was canceled.
func (d *Db) PublishImport(ctx context.Context, id, owner string, appId int64, partial string, metadata ipa.Metadata, size int64) error {
	return d.publish(ctx, id, partial, size, "UPDATE packages SET app_id=?,name=?,bundle=?,version=?,display_version=?,minimum_os=?,size=? WHERE id=? AND account_id=? AND size IS NULL",
		appId, metadata.Name, metadata.Bundle, metadata.Version, metadata.DisplayVersion, metadata.MinimumOs, size, id, owner)
}

// publish renames the file only after both rows accepted the change; a failed
// commit removes it again, so the file and its row appear together.
func (d *Db) publish(ctx context.Context, id, partial string, size int64, update string, args ...any) error {
	path := d.PackagePath(id)
	renamed := false
	err := d.tx(ctx, func(tx *sql.Tx) error {
		n, err := changed(ctx, tx, "UPDATE downloads SET status=?,received=?,total=?,speed=0,error='' WHERE package_id=? AND status IN "+running, wire.Done, size, size, id)
		if err == nil && n == 1 {
			n, err = changed(ctx, tx, update, args...)
		}
		if err != nil {
			return err
		}
		if n != 1 {
			return fault.Canceled
		}
		if err = os.Rename(partial, path); err != nil {
			return err
		}
		renamed = true
		return nil
	})
	if err != nil && renamed {
		if removeErr := os.Remove(path); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			err = errors.Join(err, removeErr)
		}
	}
	return err
}

// FlushTransfers retries terminal states that could not be written (for example
// while SQLite's volume was full). Their effective state remains visible in memory.
func (d *Db) FlushTransfers(ctx context.Context) error {
	d.terminalMu.Lock()
	defer d.terminalMu.Unlock()
	for id, end := range d.terminal {
		if err := d.finishDownload(ctx, id, end); err != nil {
			return err
		}
		delete(d.terminal, id)
	}
	return nil
}
