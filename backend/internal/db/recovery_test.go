package db

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"appleseum/backend/internal/fault"
	"appleseum/backend/internal/wire"
)

func publishedFixture(t *testing.T, s *Db) string {
	t.Helper()
	id := NewId()
	if _, err := s.conn.Exec("INSERT OR IGNORE INTO accounts(id) VALUES('owner'); INSERT INTO packages(id,account_id,app_id,name,created_at,size) VALUES(?,'owner',1,'Fixture','',1)", id); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.PackagePath(id), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestDeletionFailurePreservesLibraryAndRestoresStagedFiles(t *testing.T) {
	s := testStore(t)
	first := publishedFixture(t, s)
	second := publishedFixture(t, s)
	if err := os.Remove(s.PackagePath(second)); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(s.PackagePath(second), 0700); err != nil {
		t.Fatal(err)
	}
	if err := s.DeletePackages(context.Background(), []string{first, second}); !fault.Is(err, fault.StorageFailed) {
		t.Fatal("deletion failure was hidden", err)
	}
	for _, id := range []string{first, second} {
		if exists, err := s.PackageExists(context.Background(), id); err != nil || !exists {
			t.Fatal("failed delete lost its record", id, err)
		}
	}
	if raw, err := os.ReadFile(s.PackagePath(first)); err != nil || string(raw) != "x" {
		t.Fatal("staged file was not restored", err)
	}
}

func TestStartupRecoversFilesAndKeepsWaitingQueue(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	kept := publishedFixture(t, s)
	if err := os.Rename(s.PackagePath(kept), s.PackagePath(kept)+".delete"); err != nil {
		t.Fatal(err)
	}
	orphan, deleted := NewId(), NewId()
	for _, name := range []string{orphan + ".ipa", deleted + ".ipa.delete", ".download-fixture", ".ipa-fixture", ".import-fixture.tmp"} {
		if err := os.WriteFile(filepath.Join(s.Directory(), "packages", name), []byte("incomplete"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.conn.Exec("INSERT INTO sessions(id,scope,csrf) VALUES('local','local','fixture')"); err != nil {
		t.Fatal(err)
	}
	for i, status := range []wire.DownloadStatus{wire.Queued, wire.Downloading} {
		id := NewId()
		if err := s.BeginImport(ctx, Import{Id: id, SessionId: "local", Name: "Fixture", AppId: int64(i + 1)}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.conn.Exec("UPDATE downloads SET status=? WHERE package_id=?", status, id); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.prepare(ctx); err != nil {
		t.Fatal(err)
	}
	if raw, err := os.ReadFile(s.PackagePath(kept)); err != nil || string(raw) != "x" {
		t.Fatal("committed library file was not restored", err)
	}
	files, err := os.ReadDir(filepath.Join(s.Directory(), "packages"))
	if err != nil || len(files) != 1 {
		t.Fatal("startup left orphaned files", files, err)
	}
	var queued, failed int
	if err := s.conn.QueryRow("SELECT sum(status='queued'),sum(status='failed' AND error='interrupted') FROM downloads").Scan(&queued, &failed); err != nil || queued != 1 || failed != 1 {
		t.Fatal("wrong restart states", queued, failed, err)
	}
}
