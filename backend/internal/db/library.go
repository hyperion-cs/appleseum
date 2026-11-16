package db

import (
	"context"
	"database/sql"
	"errors"
	"os"

	"appleseum/backend/internal/fault"
	"appleseum/backend/internal/wire"
)

var librarySorts = map[string]sortKey{
	"name":      {expr: "name COLLATE APP_TEXT"},
	"status":    {expr: "status"},
	"version":   {expr: "version COLLATE APP_TEXT"},
	"size":      {expr: "size", numeric: true},
	"speed":     {expr: "speed", numeric: true},
	"createdAt": {expr: "created_at"},
}

func (d *Db) Library(ctx context.Context, scope Scope, q Query) (wire.LibraryPage, error) {
	where, filter := scope.libraryFilter(q.Mine)
	where, filter = searchFilter(packageSearch, where, filter, q.Search)
	page, err := readPage(ctx, d.conn, list{
		columns: "id,app_id,name,version,minimum_os,size,created_at,status,received,total,speed,error," +
			"COALESCE((SELECT icon FROM app_assets a WHERE a.account_id=library.account_id AND a.app_id=library.app_id),'')," +
			"? OR account_id=?",
		args:   []any{scope.ManagesAll(), scope.Owner},
		source: "library", where: where, filter: filter, sorts: librarySorts,
	}, q, func(item *wire.LibraryItem) ([]any, *string) {
		return []any{&item.Id, &item.AppId, &item.Name, &item.Version, &item.MinimumOs, &item.Size, &item.CreatedAt,
			&item.Status, &item.Received, &item.Total, &item.Speed, &item.Error, &item.Icon, &item.CanManage}, &item.Cursor
	})
	d.terminalMu.Lock()
	defer d.terminalMu.Unlock()
	for i := range page.Items {
		item := &page.Items[i]
		if end, ok := d.terminal[item.Id]; ok {
			item.Status, item.Error, item.Speed = end.status, end.code, 0
			if item.Status == wire.Canceling || item.Status == wire.Canceled {
				item.Status, item.Error = wire.Canceled, ""
			}
		}
	}
	return page, err
}

type LibraryEntry struct {
	Id, Owner string
	Status    wire.DownloadStatus
}

// LibrarySelection resolves a selection to visible entries. Explicit IDs must
// all be visible; a bounded selection fails rather than truncates.
func (d *Db) LibrarySelection(ctx context.Context, scope Scope, input wire.Selection, bounded bool) ([]LibraryEntry, error) {
	f := input.Filter
	where, args := scope.libraryFilter(f != nil && f.Mine)
	if f != nil {
		where, args = searchFilter(packageSearch, where, args, f.Query)
	} else {
		where += " AND id IN (SELECT value FROM json_each(?))"
		args = append(args, jsonArray[string](input.Ids))
	}
	query := "SELECT id,status,account_id FROM library " + where + " ORDER BY id"
	if bounded {
		query += " LIMIT ?"
		args = append(args, wire.MaxSelectionSize+1)
	}
	entries, err := collect(ctx, d.conn, func(row scanner) (LibraryEntry, error) {
		var e LibraryEntry
		return e, row.Scan(&e.Id, &e.Status, &e.Owner)
	}, query, args...)
	switch {
	case err != nil:
		return nil, err
	case bounded && len(entries) > wire.MaxSelectionSize:
		return nil, fault.SelectionTooLarge
	case f == nil && len(entries) != len(input.Ids):
		return nil, fault.NotFound
	}
	return entries, nil
}

type ExportRecord struct {
	Id, Name, Version string
	AppId             int64
}

func (d *Db) ExportRecords(ctx context.Context, ids []string) ([]ExportRecord, error) {
	return collect(ctx, d.conn, func(row scanner) (ExportRecord, error) {
		var r ExportRecord
		return r, row.Scan(&r.Id, &r.AppId, &r.Name, &r.Version)
	}, "SELECT id,app_id,name,shown_version FROM ready_packages WHERE id IN (SELECT value FROM json_each(?)) ORDER BY id", jsonArray[string](ids))
}

// DeletePackages removes published files unless a share includes one of them.
// The check and the delete are one transaction, so a share started meanwhile
// either blocks the delete or fails on its missing package.
func (d *Db) DeletePackages(ctx context.Context, ids []string) error {
	moved := []string{}
	err := d.tx(ctx, func(tx *sql.Tx) error {
		shared, err := exists(ctx, tx, "SELECT 1 FROM share_packages WHERE package_id IN (SELECT value FROM json_each(?))", jsonArray[string](ids))
		if err != nil {
			return err
		}
		if shared {
			return fault.SharedFiles
		}
		for _, id := range ids {
			path := d.PackagePath(id)
			info, err := os.Lstat(path)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return fault.StorageFailed
			}
			if err = os.Rename(path, path+".delete"); err != nil {
				return err
			}
			moved = append(moved, id)
		}
		_, err = tx.ExecContext(ctx, "DELETE FROM packages WHERE id IN (SELECT value FROM json_each(?)) AND size IS NOT NULL", jsonArray[string](ids))
		return err
	})
	if err != nil {
		for _, id := range moved {
			path := d.PackagePath(id)
			err = errors.Join(err, os.Rename(path+".delete", path))
		}
		return err
	}
	for _, id := range moved {
		err = errors.Join(err, os.Remove(d.PackagePath(id)+".delete"))
	}
	return err
}

func (d *Db) PackageExists(ctx context.Context, id string) (bool, error) {
	return exists(ctx, d.conn, "SELECT 1 FROM packages WHERE id=?", id)
}

// found turns a missing row into the API's not_found code.
func found(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return fault.NotFound
	}
	return err
}
