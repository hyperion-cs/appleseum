package db

import (
	"context"
	"fmt"
	"testing"

	"appleseum/backend/internal/fault"
	"appleseum/backend/internal/wire"
)

func TestQueueQuotaIsAtomicAfterDeduplicationAndAcrossSessions(t *testing.T) {
	for _, public := range []bool{false, true} {
		t.Run(fmt.Sprint(public), func(t *testing.T) {
			s := testStore(t)
			ctx := context.Background()
			if _, err := s.conn.Exec("INSERT INTO accounts(id) VALUES('owner'); INSERT INTO sessions(id,scope,csrf) VALUES('one','public','x'),('two','public','y')"); err != nil {
				t.Fatal(err)
			}
			limit := 512
			if public {
				limit = 128
			}
			var ids []int64
			for i := 1; i <= limit+2; i++ {
				if _, err := s.conn.Exec("INSERT INTO catalog(id,account_id,app_id,name,delisted) VALUES(?,'owner',?,'Fixture',0)", NewId(), i); err != nil {
					t.Fatal(err)
				}
				ids = append(ids, int64(i))
			}
			scope := Scope{Owner: "owner", Public: public}
			if err := s.EnqueueDownloads(ctx, scope, "one", ids[:limit], nil); err != nil {
				t.Fatal(err)
			}
			if err := s.EnqueueDownloads(ctx, scope, "two", ids[:limit], nil); err != nil {
				t.Fatal("duplicates consumed quota", err)
			}
			if err := s.EnqueueDownloads(ctx, scope, "two", ids[limit-1:], nil); err != fault.QueueFull {
				t.Fatal("cross-session quota bypass", err)
			}
			var count int
			if err := s.conn.QueryRow("SELECT count(*) FROM packages").Scan(&count); err != nil || count != limit {
				t.Fatal("rejected batch was partially inserted", count, err)
			}
			var id string
			if err := s.conn.QueryRow("SELECT id FROM packages LIMIT 1").Scan(&id); err != nil {
				t.Fatal(err)
			}
			if err := s.FinishDownload(ctx, id, wire.Canceled, ""); err != nil {
				t.Fatal(err)
			}
			if err := s.EnqueueDownloads(ctx, scope, "two", ids[limit:limit+1], nil); err != nil {
				t.Fatal("cancellation did not free queue capacity", err)
			}
		})
	}
}

func TestServerQueueQuotaCoversOtherAccounts(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if _, err := s.conn.Exec("INSERT INTO accounts(id) VALUES('owner'),('other'); INSERT INTO sessions(id,scope,csrf) VALUES('one','public','x'); INSERT INTO catalog(id,account_id,app_id,name,delisted) VALUES('app','owner',1,'App',0)"); err != nil {
		t.Fatal(err)
	}
	tx, err := s.conn.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4_096; i++ {
		id := fmt.Sprint(i)
		if _, err = tx.Exec("INSERT INTO packages(id,account_id,app_id,name,created_at) VALUES(?,'other',?,'Queued','')", id, i+1); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec("INSERT INTO downloads(package_id,session_id,status) VALUES(?,'one','queued')", id); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err = s.EnqueueDownloads(ctx, Scope{Owner: "owner", Public: true}, "one", []int64{1}, nil); err != fault.QueueFull {
		t.Fatal("global queue bypass", err)
	}
}

func TestTerminalStateSurvivesFailedDatabaseWrite(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if _, err := s.conn.Exec("INSERT INTO sessions(id,scope,csrf) VALUES('local','local','fixture')"); err != nil {
		t.Fatal(err)
	}
	id := NewId()
	if err := s.BeginImport(ctx, Import{Id: id, SessionId: "local", Name: "Fixture", AppId: -1}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.conn.Exec("CREATE TRIGGER reject_finish BEFORE UPDATE OF status ON downloads WHEN NEW.status='failed' BEGIN SELECT RAISE(ABORT,'simulated storage failure'); END"); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishDownload(ctx, id, wire.Failed, fault.StorageFull); err == nil {
		t.Fatal("write unexpectedly succeeded")
	}
	result, err := s.Library(ctx, Scope{}, page(t, "name", 10, nil))
	if err != nil || len(result.Items) != 1 || result.Items[0].Status != wire.Failed || result.Items[0].Error != fault.StorageFull {
		t.Fatal("terminal state lost", result, err)
	}
	if _, err := s.conn.Exec("DROP TRIGGER reject_finish"); err != nil {
		t.Fatal(err)
	}
	if err := s.FlushTransfers(ctx); err != nil {
		t.Fatal(err)
	}
	var status string
	if err := s.conn.QueryRow("SELECT status FROM downloads WHERE package_id=?", id).Scan(&status); err != nil || status != "failed" {
		t.Fatal(status, err)
	}
}
