// Package db owns the SQLite schema and every query; other packages never write SQL.
package db

import (
	"appleseum/backend/internal/fault"
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"

	"github.com/google/uuid"

	"appleseum/backend/internal/wire"
)

type Db struct {
	conn       *sql.DB
	dir        string
	terminalMu sync.Mutex
	terminal   map[string]transferEnd
}

type transferEnd struct {
	status wire.DownloadStatus
	code   fault.Code
}

func New(conn *sql.DB, dir string) *Db { return &Db{conn: conn, dir: dir} }

func (d *Db) Directory() string { return d.dir }

func (d *Db) PackagePath(id string) string { return filepath.Join(d.dir, "packages", id+".ipa") }

func (d *Db) IconPath(name string) string { return filepath.Join(d.dir, "icons", name) }

// NewId returns a time-ordered UUID for a new row.
func NewId() string { return uuid.Must(uuid.NewV7()).String() }

// querier is a database or a transaction; with one connection, code inside a
// transaction must use it rather than the database, or it waits on itself.
type querier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

type scanner interface{ Scan(...any) error }

func collect[T any](ctx context.Context, q querier, scan func(scanner) (T, error), query string, args ...any) ([]T, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []T{}
	for rows.Next() {
		item, err := scan(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func value[T any](row scanner) (T, error) {
	var v T
	err := row.Scan(&v)
	return v, err
}

func exists(ctx context.Context, q querier, query string, args ...any) (bool, error) {
	var found bool
	err := q.QueryRowContext(ctx, "SELECT EXISTS("+query+")", args...).Scan(&found)
	return found, err
}

// changed runs a write and reports how many rows it touched.
func changed(ctx context.Context, q querier, query string, args ...any) (int64, error) {
	result, err := q.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (d *Db) tx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := d.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// statusList is an SQL list of fixed status constants, never of user input.
func statusList(statuses ...wire.DownloadStatus) string {
	quoted := make([]string, len(statuses))
	for i, status := range statuses {
		quoted[i] = "'" + string(status) + "'"
	}
	return "(" + strings.Join(quoted, ",") + ")"
}

// activeStatuses are transfers that still hold their place in the queue.
var activeStatuses = statusList(wire.Queued, wire.Downloading, wire.Preparing, wire.Canceling)

// jsonArray binds a variable-length list as one parameter, read with json_each.
type jsonArray[T any] []T

func (values jsonArray[T]) Value() (driver.Value, error) {
	if len(values) == 0 {
		return "[]", nil
	}
	data, err := json.Marshal([]T(values))
	return string(data), err
}
