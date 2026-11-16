package db

import (
	"database/sql"
	"strings"
	"testing"

	"appleseum/backend/internal/paging"
)

func TestCurrentQueryPlans(t *testing.T) {
	s := testStore(t)
	if _, err := s.conn.Exec(`INSERT INTO accounts(id) VALUES('fixture');
 WITH RECURSIVE n(i) AS (VALUES(1) UNION ALL SELECT i+1 FROM n WHERE i<1000)
 INSERT INTO catalog(id,account_id,app_id,name,delisted,purchased_at)
 SELECT printf('%08d',i),'fixture',i,'Fixture',0,'2026-11-01' FROM n;
 INSERT INTO packages(id,account_id,app_id,name,created_at,size) VALUES('fixture','fixture',1,'Fixture','',10)`); err != nil {
		t.Fatal(err)
	}
	scope := Scope{Owner: "fixture", Public: true}
	catalog := list{columns: "app_id," + scope.catalogSize(), source: "catalog", where: "WHERE account_id=?", filter: []any{scope.Owner}}
	query, args := catalog.pageSql(scope.catalogSorts()["purchasedAt"], true, paging.Page{Limit: 5})
	assertQueryPlan(t, s.conn, query, args, "catalog_purchased", "packages_account_app")
	for _, back := range []bool{false, true} {
		query, args := versionPageSql("fixture", 1, paging.Page{Limit: 5, After: &paging.Key{Id: "5", Value: 5}, Back: back})
		assertQueryPlan(t, s.conn, query, args, "app_versions_position")
	}
	assertQueryPlan(t, s.conn, duplicateShareSql, []any{"fixture", 1, jsonArray[string]{"fixture"}}, "share_packages_package")
	assertQueryPlan(t, s.conn, "SELECT DISTINCT icon FROM app_assets", nil, "app_assets_icon")
}

func assertQueryPlan(t *testing.T, db *sql.DB, query string, args []any, indexes ...string) {
	t.Helper()
	rows, err := db.Query("EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	text := strings.Join(plan, "\n")
	if strings.Contains(text, "TEMP B-TREE") {
		t.Fatalf("hot query sorts into a temporary tree:\n%s", text)
	}
	for _, index := range indexes {
		if !strings.Contains(text, index) {
			t.Fatalf("hot query does not use %s:\n%s", index, text)
		}
	}
}
