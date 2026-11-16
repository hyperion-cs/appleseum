package db

import (
	"context"
	"database/sql"
	"errors"

	"appleseum/backend/internal/fault"
	"appleseum/backend/internal/wire"
)

const (
	duplicateShareSql = `SELECT 1 FROM share_packages candidate WHERE candidate.package_id=?
 AND (SELECT count(*) FROM share_packages WHERE share_id=candidate.share_id)=?
 AND NOT EXISTS(SELECT 1 FROM share_packages sp WHERE sp.share_id=candidate.share_id
 AND sp.package_id NOT IN (SELECT value FROM json_each(?)))`
)

// Only the service account sees other owners, so only it sorts by owner.
func (s Scope) shareSorts() map[string]sortKey {
	sorts := map[string]sortKey{
		"name":      {expr: "name COLLATE APP_TEXT"},
		"createdAt": {expr: "created_at"},
		"count":     {expr: "count", numeric: true},
	}
	if s.Service {
		sorts["owner"] = sortKey{expr: "owner_name COLLATE APP_TEXT"}
	}
	return sorts
}

func scanShare(row scanner) (wire.Share, error) {
	var item wire.Share
	return item, row.Scan(&item.Id, &item.Name, &item.Icon, &item.Count, &item.Owner, &item.CreatedAt)
}

func (d *Db) Shares(ctx context.Context, scope Scope, q Query) (wire.SharePage, error) {
	where, filter := scope.shareFilter(q.Search)
	return readPage(ctx, d.conn, list{
		columns: "id,name,icon,count,owner_name,created_at",
		source:  "share_list", where: where, filter: filter, sorts: scope.shareSorts(),
	}, q, func(item *wire.Share) ([]any, *string) {
		return []any{&item.Id, &item.Name, &item.Icon, &item.Count, &item.Owner, &item.CreatedAt}, &item.Cursor
	})
}

func (d *Db) Share(ctx context.Context, scope Scope, id string) (wire.Share, error) {
	where, args := scope.shareFilter("")
	item, err := scanShare(d.conn.QueryRowContext(ctx, "SELECT id,name,icon,count,owner_name,created_at FROM share_list "+where+" AND id=?", append(args, id)...))
	return item, found(err)
}

func (d *Db) SelectShares(ctx context.Context, scope Scope, query string) ([]string, error) {
	where, args := scope.shareFilter(query)
	ids, err := collect(ctx, d.conn, value[string], "SELECT id FROM shares "+where+" ORDER BY id LIMIT ?", append(args, wire.MaxSelectionSize+1)...)
	if err == nil && len(ids) > wire.MaxSelectionSize {
		return nil, fault.SelectionTooLarge
	}
	return ids, err
}

func (d *Db) ShareCount(ctx context.Context, scope Scope) (int64, error) {
	where, args := scope.shareFilter("")
	return value[int64](d.conn.QueryRowContext(ctx, "SELECT count(*) FROM shares "+where, args...))
}

type NewShare struct {
	Id, SessionId, Owner, Name string
	Packages                   []string
	Limit                      int64
}

// CreateShare checks the duplicate and the owner's limit in the same transaction as the insert.
func (d *Db) CreateShare(ctx context.Context, share NewShare) error {
	return d.tx(ctx, func(tx *sql.Tx) error {
		duplicate, err := exists(ctx, tx, duplicateShareSql, share.Packages[0], len(share.Packages), jsonArray[string](share.Packages))
		if err != nil {
			return err
		}
		if duplicate {
			return fault.SharingDuplicate
		}
		count, err := value[int64](tx.QueryRowContext(ctx, "SELECT count(*) FROM shares WHERE owner_id=?", share.Owner))
		if err != nil {
			return err
		}
		if count >= share.Limit {
			return fault.SharingLimit
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO shares(id,created_at,creator_session_id,owner_id,owner_name) VALUES(?,?,?,?,?)", share.Id, now(), share.SessionId, share.Owner, share.Name); err != nil {
			return err
		}
		n, err := changed(ctx, tx, "INSERT INTO share_packages(share_id,package_id) SELECT ?,id FROM ready_packages WHERE id IN (SELECT value FROM json_each(?))", share.Id, jsonArray[string](share.Packages))
		if err == nil && n != int64(len(share.Packages)) {
			return fault.MissingPackage
		}
		return err
	})
}

// ClearShares ends every share. It also drops package rows whose share is
// already gone, which a connection without foreign keys could once leave.
func (d *Db) ClearShares(ctx context.Context) error {
	_, err := d.conn.ExecContext(ctx, "DELETE FROM share_packages; DELETE FROM shares")
	return err
}

// StopShares ends the selected shares. Stopping one that already ended is not
// an error; naming another owner's share is, as if it did not exist.
func (d *Db) StopShares(ctx context.Context, scope Scope, input wire.Selection) error {
	if f := input.Filter; f != nil {
		where, args := scope.shareFilter(f.Query)
		_, err := d.conn.ExecContext(ctx, "DELETE FROM shares "+where, args...)
		return err
	}
	where, args := scope.shareFilter("")
	ids := jsonArray[string](input.Ids)
	return d.tx(ctx, func(tx *sql.Tx) error {
		if scope.Public && !scope.Service {
			foreign, err := exists(ctx, tx, "SELECT 1 FROM shares WHERE owner_id!=? AND id IN (SELECT value FROM json_each(?))", scope.Owner, ids)
			if err != nil {
				return err
			}
			if foreign {
				return fault.NotFound
			}
		}
		_, err := tx.ExecContext(ctx, "DELETE FROM shares "+where+" AND id IN (SELECT value FROM json_each(?))", append(args, ids)...)
		return err
	})
}

type HostedFile struct {
	Id, Bundle, Version, Name string
	Size                      int64
}

func (d *Db) HostedFiles(ctx context.Context, shareId string) ([]HostedFile, error) {
	return collect(ctx, d.conn, func(row scanner) (HostedFile, error) {
		var f HostedFile
		return f, row.Scan(&f.Id, &f.Bundle, &f.Version, &f.Name, &f.Size)
	}, "SELECT p.id,p.bundle,p.version,p.name,p.size FROM ready_packages p JOIN share_packages sp ON sp.package_id=p.id WHERE sp.share_id=? ORDER BY p.id", shareId)
}

// HostedSize returns the size of a package while the share includes it.
func (d *Db) HostedSize(ctx context.Context, shareId, fileId string) (int64, error) {
	size, err := value[int64](d.conn.QueryRowContext(ctx, "SELECT p.size FROM ready_packages p JOIN share_packages sp ON sp.package_id=p.id WHERE sp.share_id=? AND sp.package_id=?", shareId, fileId))
	if errors.Is(err, sql.ErrNoRows) {
		return 0, fault.NotFound
	}
	return size, err
}
