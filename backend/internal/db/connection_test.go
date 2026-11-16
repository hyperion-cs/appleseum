package db

import (
	"context"
	"testing"
	"time"
)

// database/sql opens a new connection after some failures, such as a canceled
// query. Each one must still enforce foreign keys, or ending a share would
// leave its package rows behind and the same files could never be shared again.
func TestEveryConnectionEnforcesTheSchema(t *testing.T) {
	s := testStore(t)
	if _, err := s.conn.Exec(`INSERT INTO accounts(id) VALUES('fixture');
 INSERT INTO packages(id,account_id,app_id,name,created_at,size) VALUES('fixture','fixture',1,'Fixture','',10);
 INSERT INTO shares(id,created_at) VALUES('share','');
 INSERT INTO share_packages(share_id,package_id) VALUES('share','fixture')`); err != nil {
		t.Fatal(err)
	}
	// Every later query runs on a connection opened after this point.
	s.conn.SetConnMaxLifetime(time.Nanosecond)
	time.Sleep(time.Millisecond)
	for pragma, want := range map[string]int{"foreign_keys": 1, "busy_timeout": 5_000, "secure_delete": 1} {
		var got int
		if err := s.conn.QueryRow("PRAGMA " + pragma).Scan(&got); err != nil || got != want {
			t.Fatalf("new connection: %s=%d, want %d (%v)", pragma, got, want, err)
		}
	}
	if err := s.ClearShares(context.Background()); err != nil {
		t.Fatal(err)
	}
	var left int
	if err := s.conn.QueryRow("SELECT count(*) FROM share_packages").Scan(&left); err != nil || left != 0 {
		t.Fatalf("ended share left %d package rows (%v)", left, err)
	}
}

func TestClearSharesDropsOrphanPackageRows(t *testing.T) {
	s := testStore(t)
	conn, err := s.conn.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	// The state an earlier build left: package rows of a share that is gone.
	if _, err := conn.ExecContext(context.Background(), `PRAGMA foreign_keys=OFF;
 INSERT INTO accounts(id) VALUES('fixture');
 INSERT INTO packages(id,account_id,app_id,name,created_at,size) VALUES('fixture','fixture',1,'Fixture','',10);
 INSERT INTO share_packages(share_id,package_id) VALUES('gone','fixture');
 PRAGMA foreign_keys=ON`); err != nil {
		t.Fatal(err)
	}
	conn.Close()
	if err := s.ClearShares(context.Background()); err != nil {
		t.Fatal(err)
	}
	var left int
	if err := s.conn.QueryRow("SELECT count(*) FROM share_packages").Scan(&left); err != nil || left != 0 {
		t.Fatalf("orphan package rows survived: %d (%v)", left, err)
	}
}
