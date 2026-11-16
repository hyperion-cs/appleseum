package db

import (
	"context"

	"appleseum/backend/internal/wire"
)

func (d *Db) Preferences(ctx context.Context, sessionId string) (wire.Preferences, error) {
	var p wire.Preferences
	err := d.conn.QueryRowContext(ctx, "SELECT language,theme,revision FROM preferences WHERE session_id=?", sessionId).Scan(&p.Language, &p.Theme, &p.Revision)
	return p, err
}

// view is one row of table_preferences; each table uses only its own filter.
type view struct {
	table          wire.Table
	sort           string
	direction      wire.Direction
	delisted, mine bool
	revision       int64
}

func (d *Db) Tables(ctx context.Context, sessionId string) (wire.Tables, error) {
	rows, err := collect(ctx, d.conn, func(r scanner) (view, error) {
		var v view
		return v, r.Scan(&v.table, &v.sort, &v.direction, &v.delisted, &v.mine, &v.revision)
	}, "SELECT resource,sort,direction,delisted,mine,revision FROM table_preferences WHERE session_id=?", sessionId)
	var tables wire.Tables
	for _, v := range rows {
		switch v.table {
		case wire.CatalogTable:
			tables.Catalog = wire.Saved[wire.CatalogView]{Value: wire.CatalogView{Sort: v.sort, Direction: v.direction, Delisted: v.delisted}, Revision: v.revision}
		case wire.LibraryTable:
			tables.Library = wire.Saved[wire.LibraryView]{Value: wire.LibraryView{Sort: v.sort, Direction: v.direction, Mine: v.mine}, Revision: v.revision}
		case wire.SharesTable:
			tables.Shares = wire.Saved[wire.ShareView]{Value: wire.ShareView{Sort: v.sort, Direction: v.direction}, Revision: v.revision}
		}
	}
	return tables, err
}

func (d *Db) saveView(ctx context.Context, sessionId string, v view) (int64, error) {
	return value[int64](d.conn.QueryRowContext(ctx, "UPDATE table_preferences SET sort=?,direction=?,delisted=?,mine=?,revision=revision+1 WHERE session_id=? AND resource=? RETURNING revision",
		v.sort, v.direction, v.delisted, v.mine, sessionId, v.table))
}

func (d *Db) SaveCatalogView(ctx context.Context, sessionId string, v wire.CatalogView) (wire.Saved[wire.CatalogView], error) {
	revision, err := d.saveView(ctx, sessionId, view{table: wire.CatalogTable, sort: v.Sort, direction: v.Direction, delisted: v.Delisted})
	return wire.Saved[wire.CatalogView]{Value: v, Revision: revision}, err
}

func (d *Db) SaveLibraryView(ctx context.Context, sessionId string, v wire.LibraryView) (wire.Saved[wire.LibraryView], error) {
	revision, err := d.saveView(ctx, sessionId, view{table: wire.LibraryTable, sort: v.Sort, direction: v.Direction, mine: v.Mine})
	return wire.Saved[wire.LibraryView]{Value: v, Revision: revision}, err
}

func (d *Db) SaveShareView(ctx context.Context, sessionId string, v wire.ShareView) (wire.Saved[wire.ShareView], error) {
	revision, err := d.saveView(ctx, sessionId, view{table: wire.SharesTable, sort: v.Sort, direction: v.Direction})
	return wire.Saved[wire.ShareView]{Value: v, Revision: revision}, err
}

func (d *Db) SavePreferences(ctx context.Context, sessionId string, input wire.PreferencesInput) (wire.Preferences, error) {
	result := wire.Preferences{Language: input.Language, Theme: input.Theme}
	err := d.conn.QueryRowContext(ctx, "UPDATE preferences SET language=?,theme=?,revision=revision+1 WHERE session_id=? RETURNING revision", input.Language, input.Theme, sessionId).Scan(&result.Revision)
	return result, err
}

// Sortable reports whether a table can be ordered by key for this scope.
func (s Scope) Sortable(table wire.Table, key string) bool {
	var sorts map[string]sortKey
	switch table {
	case wire.CatalogTable:
		sorts = s.catalogSorts()
	case wire.LibraryTable:
		sorts = librarySorts
	case wire.SharesTable:
		sorts = s.shareSorts()
	}
	_, ok := sorts[key]
	return ok
}
