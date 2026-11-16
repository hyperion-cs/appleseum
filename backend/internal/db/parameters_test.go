package db

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"appleseum/backend/internal/paging"
	"appleseum/backend/internal/wire"
)

func testStore(t testing.TB) *Db {
	t.Helper()
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "packages"), 0700); err != nil {
		t.Fatal(err)
	}
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func page(t testing.TB, sort string, limit int, after *paging.Key) Query {
	t.Helper()
	return Query{Sort: sort, Page: paging.Page{Limit: limit, After: after}}
}

func TestSqlParametersKeepInputsLiteral(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	attack := `x'); DROP TABLE packages; --`
	if _, err := s.conn.Exec("INSERT INTO accounts(id) VALUES(?),(?)", attack, "other"); err != nil {
		t.Fatal(err)
	}
	first, second := NewId(), NewId()
	for i, owner := range []string{attack, "other"} {
		id := []string{first, second}[i]
		if _, err := s.conn.Exec("INSERT INTO packages(id,account_id,app_id,name,created_at,size) VALUES(?,?,?,?,'',1)", id, owner, i+1, attack); err != nil {
			t.Fatal(err)
		}
	}
	scope := Scope{Owner: attack, Public: true}
	q := page(t, "name", 10, nil)
	q.Search = attack
	result, err := s.Library(ctx, scope, q)
	if err != nil || result.Total != 1 || len(result.Items) != 1 || result.Items[0].Id != first {
		t.Fatalf("input changed SQL scope: %+v, %v", result, err)
	}
	if _, err := s.Library(ctx, scope, page(t, attack, 10, nil)); err == nil {
		t.Fatal("accepted SQL as a sort key")
	}
	if _, err := s.Library(ctx, scope, page(t, "name", 10, &paging.Key{Id: attack, Value: attack})); err == nil {
		t.Fatal("invalid cursor accepted")
	}
	if _, err := s.Library(ctx, scope, page(t, "name", 10, &paging.Key{Id: first, Value: attack})); err != nil {
		t.Fatal(err)
	}
	exports, err := s.ExportRecords(ctx, []string{attack, first})
	if err != nil || len(exports) != 1 || exports[0].Id != first {
		t.Fatalf("ID list escaped its parameter: %+v, %v", exports, err)
	}
	if empty, err := s.ExportRecords(ctx, nil); err != nil || len(empty) != 0 {
		t.Fatalf("empty ID list: %+v, %v", empty, err)
	}
	share := NewId()
	if err := s.CreateShare(ctx, NewShare{Id: share, SessionId: "missing", Owner: attack, Name: attack, Packages: []string{first}, Limit: 8}); err == nil {
		t.Fatal("foreign-key failure should roll back share")
	}
	if _, err := s.conn.Exec("INSERT INTO sessions(id,scope,csrf) VALUES(?,'local',?)", attack, attack); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateShare(ctx, NewShare{Id: share, SessionId: attack, Owner: attack, Name: attack, Packages: []string{first}, Limit: 8}); err != nil {
		t.Fatal(err)
	}
	if err := s.StopShares(ctx, scope, wire.Selection{Ids: []string{attack}}); err != nil {
		t.Fatal(err)
	}
	if count, err := s.ShareCount(ctx, scope); err != nil || count != 1 {
		t.Fatalf("malformed ID changed shares: %d, %v", count, err)
	}
	if err := s.StopShares(ctx, scope, wire.Selection{Filter: &wire.Filter{Query: `' OR 1=1 --`}}); err != nil {
		t.Fatal(err)
	}
	if count, err := s.ShareCount(ctx, scope); err != nil || count != 1 {
		t.Fatalf("search changed shares: %d, %v", count, err)
	}
	var count int
	if err := s.conn.QueryRow("SELECT count(*) FROM packages").Scan(&count); err != nil || count != 2 {
		t.Fatalf("SQL schema/data damaged: %d, %v", count, err)
	}
}

func TestClaimDownloadIsAtomicAndScoped(t *testing.T) {
	const jobs = 32

	s := testStore(t)
	if _, err := s.conn.Exec("INSERT INTO accounts(id) VALUES('owner'),('other'); INSERT INTO sessions(id,scope,csrf) VALUES('local','local','fixture'),('remote','public','fixture')"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i <= jobs; i++ {
		owner, session := "owner", "local"
		if i == jobs {
			owner, session = "other", "remote"
		}
		id := fmt.Sprintf("package-%03d", i)
		if _, err := s.conn.Exec("INSERT INTO packages(id,account_id,app_id,name,created_at) VALUES(?,?,?,'Fixture','')", id, owner, i+1); err != nil {
			t.Fatal(err)
		}
		if _, err := s.conn.Exec("INSERT INTO downloads(package_id,session_id,status) VALUES(?,?,'queued')", id, session); err != nil {
			t.Fatal(err)
		}
	}
	seen := sync.Map{}
	var wg sync.WaitGroup
	for i := 0; i < jobs; i++ {
		wg.Go(func() {
			job, ok, err := s.ClaimDownload(context.Background(), Scope{Owner: "owner", Public: true}, "local")
			if err != nil || !ok {
				t.Error(err)
				return
			}
			if _, duplicate := seen.LoadOrStore(job.Id, true); duplicate {
				t.Errorf("job claimed twice: %s", job.Id)
			}
			if job.AppId < 1 || job.AppId > jobs {
				t.Errorf("wrong owner or job metadata: %+v", job)
			}
		})
	}
	wg.Wait()
	if _, ok, err := s.ClaimDownload(context.Background(), Scope{Owner: "owner", Public: true}, "local"); err != nil || ok {
		t.Fatalf("exhausted queue: %v", err)
	}
	var active int
	if err := s.conn.QueryRow("SELECT count(*) FROM downloads WHERE status='downloading'").Scan(&active); err != nil || active != jobs {
		t.Fatalf("non-atomic claim: %d, %v", active, err)
	}
	if job, _, err := s.ClaimDownload(context.Background(), Scope{Owner: "other", Public: true}, "remote"); err != nil || job.AppId != jobs+1 {
		t.Fatalf("other session queue changed: %+v, %v", job, err)
	}
}

func TestVersionBatchPreservesMetadataAndOrdering(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if _, err := s.conn.Exec("INSERT INTO accounts(id) VALUES('owner')"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveVersionList(ctx, "owner", 1, "3", []string{"1", "2", "2", "invalid", "3"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.conn.Exec("UPDATE app_versions SET version='1.2.3',minimum_os='17.0' WHERE version_id='2'"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveVersionList(ctx, "owner", 1, "4", []string{"2", "3", "4"}); err != nil {
		t.Fatal(err)
	}
	list, err := s.VersionList(ctx, "owner", 1)
	if err != nil {
		t.Fatal(err)
	}
	versions, err := s.Versions(ctx, "owner", 1, list, paging.Page{Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if versions.Total != 3 || len(versions.Items) != 3 || versions.Items[0].Id != "4" || versions.Items[1].Id != "3" || versions.Items[2].Id != "2" {
		t.Fatalf("batch order/deduplication lost: %+v", versions)
	}
	if versions.Items[2].Version != "1.2.3" || versions.Items[2].MinimumOs != "17.0" {
		t.Fatalf("cached metadata lost: %+v", versions.Items[2])
	}
}
