package db

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"
	"modernc.org/sqlite"

	"appleseum/backend/internal/artwork"
	"appleseum/backend/internal/fault"
	"appleseum/backend/internal/wire"
)

const (
	schemaVersion = 1
	// The driver runs these on every connection it opens. database/sql replaces
	// a connection after some failures, such as a canceled query, and a PRAGMA
	// run once at startup would not reach the new one: SQLite would then stop
	// enforcing foreign keys, so ON DELETE CASCADE would leave orphan rows.
	connectionPragmas = "?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=secure_delete(1)"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Open applies migrations and recovers what an interrupted run left behind:
// unfinished transfers, partial files and icons nothing refers to.
func Open(dir string) (*Db, error) {
	if info, err := os.Stat(filepath.Join(dir, "packages")); err != nil || !info.IsDir() {
		return nil, fmt.Errorf("package storage directory is unavailable: %s", dir)
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "metadata.db")+connectionPragmas)
	if err != nil {
		return nil, err
	}
	// SQLite allows one writer; one connection also orders every transaction.
	db.SetMaxOpenConns(1)
	s := New(db, dir)
	if err = s.prepare(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (d *Db) Close() error { return d.conn.Close() }

func (d *Db) prepare(ctx context.Context) error {
	if err := removeFiles(filepath.Join(d.dir, "packages"), func(name string) bool {
		return strings.HasSuffix(name, ".partial") || strings.HasSuffix(name, ".tmp") || strings.HasPrefix(name, ".download-") || strings.HasPrefix(name, ".ipa-") || strings.HasPrefix(name, ".space-")
	}); err != nil {
		return err
	}
	if err := d.migrate(ctx); err != nil {
		return err
	}
	if err := os.Chmod(filepath.Join(d.dir, "metadata.db"), 0o600); err != nil {
		return err
	}
	if err := d.recoverPackages(ctx); err != nil {
		return err
	}
	if _, err := d.conn.ExecContext(ctx, "UPDATE downloads SET status=?,speed=0,error=? WHERE status IN "+statusList(wire.Downloading, wire.Preparing), wire.Failed, fault.Interrupted); err != nil {
		return err
	}
	if _, err := d.conn.ExecContext(ctx, "UPDATE downloads SET status=?,speed=0 WHERE status=?", wire.Canceled, wire.Canceling); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(d.dir, "icons"), 0o700); err != nil {
		return err
	}
	icons, err := collect(ctx, d.conn, value[string], "SELECT DISTINCT icon FROM app_assets")
	if err != nil {
		return err
	}
	referenced := make(map[string]bool, len(icons))
	for _, icon := range icons {
		referenced[icon] = true
	}
	return removeFiles(filepath.Join(d.dir, "icons"), func(name string) bool {
		return artwork.ValidName(name) && !referenced[name] || strings.HasPrefix(name, ".icon-")
	})
}

func (d *Db) migrate(ctx context.Context) error {
	var version int
	if err := d.conn.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version > schemaVersion {
		return fmt.Errorf("database schema %d is newer than this build", version)
	}
	for next := version + 1; next <= schemaVersion; next++ {
		script, err := migrations.ReadFile(fmt.Sprintf("migrations/%03d.sql", next))
		if err != nil {
			return err
		}
		if err = d.tx(ctx, func(tx *sql.Tx) error { _, err := tx.ExecContext(ctx, string(script)); return err }); err != nil {
			return fmt.Errorf("migration %d: %w", next, err)
		}
	}
	return nil
}

func removeFiles(dir string, remove func(string) bool) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() && remove(entry.Name()) {
			if err = os.Remove(filepath.Join(dir, entry.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}

func init() {
	var mu sync.Mutex
	sorter := collate.New(language.Russian, collate.IgnoreCase, collate.Numeric)
	// A collator is not safe for concurrent use.
	if err := sqlite.RegisterCollationUtf8("APP_TEXT", func(a, b string) int { mu.Lock(); defer mu.Unlock(); return sorter.CompareString(a, b) }); err != nil {
		panic(err)
	}
	if err := sqlite.RegisterDeterministicScalarFunction("fold", 1, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		text, ok := args[0].(string)
		if !ok {
			return "", nil
		}
		return fold(text), nil
	}); err != nil {
		panic(err)
	}
}

// Stats describes the connection pool for diagnostics.
func (d *Db) Stats() sql.DBStats { return d.conn.Stats() }
