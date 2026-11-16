package db

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"appleseum/backend/internal/fault"
	"appleseum/backend/internal/wire"

	"github.com/hyperion-cs/appleseum/applekit/ipa"
)

func BenchmarkEnqueueDownloads(b *testing.B) {
	dir := b.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "packages"), 0700); err != nil {
		b.Fatal(err)
	}
	s, err := Open(dir)
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	db := s.conn
	tx, err := db.Begin()
	if err != nil {
		b.Fatal(err)
	}
	defer tx.Rollback()
	var ids []int64
	for id := int64(1); id <= 500; id++ {
		if _, err := tx.Exec("INSERT INTO catalog(id,account_id,app_id,name,delisted) VALUES(?,'fixture',?,'Fixture',0)", strconv.FormatInt(id, 10), id); err != nil {
			b.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := tx.Commit(); err != nil {
		b.Fatal(err)
	}
	if _, err = db.Exec("INSERT INTO accounts(id) VALUES('fixture');INSERT INTO sessions(id,scope,csrf) VALUES('local','local','fixture')"); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := s.EnqueueDownloads(context.Background(), Scope{Owner: "fixture"}, "local", ids, nil); err != nil {
			b.Fatal(err)
		}
		b.StopTimer()
		if _, err := db.Exec("DELETE FROM packages"); err != nil {
			b.Fatal(err)
		}
		b.StartTimer()
	}
}

func TestPackagePublicationRollsBackFileAndMetadataAndCanRetry(t *testing.T) {
	for _, imported := range []bool{false, true} {
		t.Run(map[bool]string{false: "download", true: "import"}[imported], func(t *testing.T) {
			s := testStore(t)
			ctx := context.Background()
			id := NewId()
			partial := filepath.Join(s.Directory(), "packages", id+".partial")
			raw := []byte("synthetic package")
			metadata := ipa.Metadata{Bundle: "test.publication", Name: "Published app", Version: "7", DisplayVersion: "1.2.3", MinimumOs: "17.0"}
			if _, err := s.conn.Exec("INSERT INTO sessions(id,scope,csrf) VALUES('local','local','fixture')"); err != nil {
				t.Fatal(err)
			}
			if err := s.BeginImport(ctx, Import{Id: id, SessionId: "local", Name: "Pending", AppId: -1, Total: int64(len(raw))}); err != nil {
				t.Fatal(err)
			}
			if _, err := s.conn.Exec(`CREATE TRIGGER refuse_completion BEFORE UPDATE OF status ON downloads
 WHEN NEW.status='done' BEGIN SELECT RAISE(ABORT,'fixture completion failure'); END`); err != nil {
				t.Fatal(err)
			}
			publish := func() error {
				if err := os.WriteFile(partial, raw, 0o600); err != nil {
					t.Fatal(err)
				}
				if imported {
					return s.PublishImport(ctx, id, "", -2, partial, metadata, int64(len(raw)))
				}
				return s.PublishDownload(ctx, id, partial, metadata, int64(len(raw)))
			}
			read := func() (size sql.NullInt64, version, status string) {
				t.Helper()
				if err := s.conn.QueryRow("SELECT size,display_version,status FROM packages JOIN downloads ON package_id=id WHERE id=?", id).Scan(&size, &version, &status); err != nil {
					t.Fatal(err)
				}
				return
			}
			if err := publish(); err == nil {
				t.Fatal("publication unexpectedly succeeded")
			}
			if _, err := os.Stat(s.PackagePath(id)); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("failed publication left an IPA", err)
			}
			if size, version, status := read(); size.Valid || version != "" || status != "downloading" {
				t.Fatalf("failed publication changed metadata: %v %q %q", size, version, status)
			}
			if _, err := s.conn.Exec("DROP TRIGGER refuse_completion"); err != nil {
				t.Fatal(err)
			}
			if err := publish(); err != nil {
				t.Fatal("retry could not reuse storage", err)
			}
			if size, version, status := read(); size.Int64 != int64(len(raw)) || version != metadata.DisplayVersion || status != "done" {
				t.Fatalf("incomplete publication: %v %q %q", size, version, status)
			}
			if data, err := os.ReadFile(s.PackagePath(id)); err != nil || string(data) != string(raw) {
				t.Fatal("published bytes changed", err)
			}
		})
	}
}

func TestCanceledTransferIsNotPublished(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	id := NewId()
	if _, err := s.conn.Exec("INSERT INTO sessions(id,scope,csrf) VALUES('local','local','fixture')"); err != nil {
		t.Fatal(err)
	}
	if err := s.BeginImport(ctx, Import{Id: id, SessionId: "local", Name: "Pending", AppId: -1}); err != nil {
		t.Fatal(err)
	}
	if running, err := s.CancelDownload(ctx, Scope{}, id); err != nil || !running {
		t.Fatal("running transfer not marked canceling", running, err)
	}
	partial := filepath.Join(s.Directory(), "packages", id+".partial")
	if err := os.WriteFile(partial, []byte("late"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.PublishImport(ctx, id, "", -2, partial, ipa.Metadata{Bundle: "late"}, 4); !errors.Is(err, fault.Canceled) {
		t.Fatal("canceled upload published", err)
	}
	if _, err := os.Stat(s.PackagePath(id)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("canceled upload left an IPA", err)
	}
	if err := s.FinishDownload(ctx, id, wire.Failed, fault.ImportFailed); err != nil {
		t.Fatal(err)
	}
	var status string
	if err := s.conn.QueryRow("SELECT status FROM downloads WHERE package_id=?", id).Scan(&status); err != nil || status != string(wire.Canceled) {
		t.Fatal("a late failure replaced the cancellation", status, err)
	}
}
