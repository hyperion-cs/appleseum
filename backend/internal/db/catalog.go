package db

import (
	"context"
	"database/sql"
	"strconv"

	"github.com/hyperion-cs/appleseum/applekit"

	"appleseum/backend/internal/fault"
	"appleseum/backend/internal/wire"
)

// ReplaceCatalog stores the account's purchases, keeping each app's row key,
// and resets this session's catalog sorting to the newest purchases.
func (d *Db) ReplaceCatalog(ctx context.Context, owner, sessionId string, apps []applekit.CatalogApp) error {
	return d.tx(ctx, func(tx *sql.Tx) error {
		type existing struct {
			app int64
			key string
		}
		rows, err := collect(ctx, tx, func(row scanner) (existing, error) {
			var e existing
			return e, row.Scan(&e.app, &e.key)
		}, "SELECT app_id,id FROM catalog WHERE account_id=?", owner)
		if err != nil {
			return err
		}
		keys := make(map[int64]string, len(rows))
		for _, row := range rows {
			keys[row.app] = row.key
		}
		if _, err = tx.ExecContext(ctx, "DELETE FROM catalog WHERE account_id=?", owner); err != nil {
			return err
		}
		insert, err := tx.PrepareContext(ctx, "INSERT INTO catalog(id,account_id,app_id,name,delisted,size,purchased_at) VALUES(?,?,?,?,?,?,?)")
		if err != nil {
			return err
		}
		defer insert.Close()
		for _, app := range apps {
			key := keys[app.Id]
			if key == "" {
				key = NewId()
			}
			if _, err = insert.ExecContext(ctx, key, owner, app.Id, app.Name, app.Delisted, app.Size, app.PurchasedAt); err != nil {
				return err
			}
		}
		_, err = tx.ExecContext(ctx, "UPDATE table_preferences SET sort='purchasedAt',direction=?,revision=revision+1 WHERE session_id=? AND resource=?", wire.Descending, sessionId, wire.CatalogTable)
		return err
	})
}

func (s Scope) catalogSorts() map[string]sortKey {
	return map[string]sortKey{
		"name":        {expr: "name COLLATE APP_TEXT"},
		"status":      {expr: "delisted", numeric: true},
		"size":        {expr: "COALESCE(" + s.catalogSize() + ",-1)", numeric: true},
		"purchasedAt": {expr: "purchased_at"},
	}
}

func (d *Db) Catalog(ctx context.Context, scope Scope, q Query) (wire.CatalogPage, error) {
	where := "WHERE account_id=?"
	if q.Delisted {
		where += " AND delisted=1"
	}
	where, filter := searchFilter(catalogSearch, where, []any{scope.Owner}, q.Search)
	return readPage(ctx, d.conn, list{
		columns: "app_id,name,delisted," + scope.catalogSize() + ",purchased_at," +
			"COALESCE((SELECT icon FROM app_assets a WHERE a.account_id=catalog.account_id AND a.app_id=catalog.app_id),'')," +
			"EXISTS(SELECT 1 " + scope.catalogPackages() + ")," + scope.catalogUpdateAvailable(),
		source: "catalog", where: where, filter: filter, sorts: scope.catalogSorts(),
	}, q, func(item *wire.CatalogItem) ([]any, *string) {
		return []any{&item.Id, &item.Name, &item.Delisted, &item.Size, &item.PurchasedAt, &item.Icon, &item.InLibrary, &item.UpdateAvailable}, &item.Cursor
	})
}

func (d *Db) CatalogName(ctx context.Context, owner string, appId int64) (string, error) {
	name, err := value[string](d.conn.QueryRowContext(ctx, "SELECT name FROM catalog WHERE account_id=? AND app_id=?", owner, appId))
	return name, found(err)
}

func (d *Db) CatalogCounts(ctx context.Context, owner string) (apps, delisted int64, err error) {
	err = d.conn.QueryRowContext(ctx, "SELECT count(*),COALESCE(sum(delisted),0) FROM catalog WHERE account_id=?", owner).Scan(&apps, &delisted)
	return
}

func (d *Db) HasCatalog(ctx context.Context, owner string) (bool, error) {
	return exists(ctx, d.conn, "SELECT 1 FROM catalog WHERE account_id=?", owner)
}

// ClearCatalog forgets the purchase list and returns icons no longer used by
// any app, which the caller may delete.
func (d *Db) ClearCatalog(ctx context.Context, owner string) ([]string, error) {
	var unused []string
	err := d.tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "DELETE FROM catalog WHERE account_id=?", owner); err != nil {
			return err
		}
		removed, err := collect(ctx, tx, value[string], `DELETE FROM app_assets AS a WHERE account_id=?
 AND NOT EXISTS(SELECT 1 FROM ready_packages f WHERE f.account_id=a.account_id AND f.app_id=a.app_id)
 AND NOT EXISTS(SELECT 1 FROM transfers j WHERE j.account_id=a.account_id AND j.app_id=a.app_id)
 RETURNING icon`, owner)
		if err != nil {
			return err
		}
		for _, icon := range removed {
			used, err := exists(ctx, tx, "SELECT 1 FROM app_assets WHERE icon=?", icon)
			if err != nil {
				return err
			}
			if !used {
				unused = append(unused, icon)
			}
		}
		return nil
	})
	return unused, err
}

// CatalogSelection resolves a selection to app ids, newest first. With
// downloadable set it keeps only missing latest versions without active jobs.
func (d *Db) CatalogSelection(ctx context.Context, scope Scope, input wire.Selection, downloadable bool) ([]int64, error) {
	where, args := "WHERE account_id=?", []any{scope.Owner}
	if downloadable {
		where += " AND " + scope.catalogDownloadable()
	}
	if f := input.Filter; f != nil {
		where, args = searchFilter(catalogSearch, where, args, f.Query)
		if f.Delisted {
			where += " AND delisted=1"
		}
	} else {
		ids := make(jsonArray[int64], len(input.Ids))
		for i, id := range input.Ids {
			n, err := strconv.ParseInt(id, 10, 64)
			if err != nil || n <= 0 {
				return nil, fault.InvalidRequest
			}
			ids[i] = n
		}
		where += " AND app_id IN (SELECT value FROM json_each(?))"
		args = append(args, ids)
	}
	apps, err := collect(ctx, d.conn, value[int64], "SELECT app_id FROM catalog "+where+" ORDER BY id DESC LIMIT ?", append(args, wire.MaxSelectionSize+1)...)
	if err == nil && len(apps) > wire.MaxSelectionSize {
		return nil, fault.SelectionTooLarge
	}
	return apps, err
}

// SavedCatalogApps limits update checks to IPAs visible to this account.
func (d *Db) SavedCatalogApps(ctx context.Context, scope Scope) ([]int64, error) {
	return collect(ctx, d.conn, value[int64], "SELECT app_id FROM catalog WHERE account_id=? AND EXISTS(SELECT 1 "+scope.catalogPackages()+") ORDER BY app_id", scope.Owner)
}
