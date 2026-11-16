package db

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/hyperion-cs/appleseum/applekit"
)

func TestReplaceCatalogKeepsKeysAndRollsBack(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "packages"), 0o700); err != nil {
		t.Fatal(err)
	}
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	db := s.conn
	ctx := context.Background()
	if err := s.ReplaceCatalog(ctx, "owner", "", []applekit.CatalogApp{{Id: 1, Name: "First"}, {Id: 2, Name: "Removed"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceCatalog(ctx, "other", "", []applekit.CatalogApp{{Id: 1, Name: "Other owner"}}); err != nil {
		t.Fatal(err)
	}
	var key string
	if err := db.QueryRow("SELECT id FROM catalog WHERE account_id='owner' AND app_id=1").Scan(&key); err != nil {
		t.Fatal(err)
	}
	apps := []applekit.CatalogApp{{Id: 1, Name: "Updated", Delisted: true}, {Id: 3, Name: "New"}}
	if err := s.ReplaceCatalog(ctx, "owner", "", apps); err != nil {
		t.Fatal(err)
	}
	var after, name string
	var count int
	if err := db.QueryRow("SELECT id,name FROM catalog WHERE account_id='owner' AND app_id=1").Scan(&after, &name); err != nil {
		t.Fatal(err)
	}
	if after != key || name != "Updated" {
		t.Fatal("existing key or new fields lost")
	}
	if err := db.QueryRow("SELECT count(*) FROM catalog").Scan(&count); err != nil || count != 3 {
		t.Fatalf("other owner or replacement lost: %d, %v", count, err)
	}
	if err := s.ReplaceCatalog(ctx, "owner", "", []applekit.CatalogApp{{Id: 4}, {Id: 4}}); err == nil {
		t.Fatal("duplicate app should fail")
	}
	if err := db.QueryRow("SELECT name FROM catalog WHERE id=?", key).Scan(&name); err != nil || name != "Updated" {
		t.Fatalf("failed replacement damaged previous catalog: %v", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := s.ReplaceCatalog(canceled, "owner", "", nil); err == nil {
		t.Fatal("canceled operation committed")
	}
}
