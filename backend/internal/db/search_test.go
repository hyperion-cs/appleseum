package db

import (
	"context"
	"database/sql"
	"fmt"
	"math/rand/v2"
	"slices"
	"testing"
	"unicode/utf8"

	"appleseum/backend/internal/paging"
	"appleseum/backend/internal/wire"
)

func searchResults(t testing.TB, db *sql.DB, source searchSource, query string) []string {
	t.Helper()
	text, args := searchIds(source, query)
	return queryStrings(t, db, text+" ORDER BY d.entity_id", args...)
}

func queryStrings(t testing.TB, db *sql.DB, query string, args ...any) []string {
	t.Helper()
	rows, err := db.Query(query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	values := []string{}
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			t.Fatal(err)
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return values
}

func TestSearchMatchesLiteralSubstrings(t *testing.T) {
	s := testStore(t)
	if _, err := s.conn.Exec("INSERT INTO accounts(id) VALUES('fixture')"); err != nil {
		t.Fatal(err)
	}
	names := []string{"Ёлка", "Елка", "ИМПОРТ IPA", `a"b %_[x]`, "two words", "中文工具", "🧭地图", "İßKΣσς", "e\u0301 café", "abcde", "abc cde bcd", `x'); DROP TABLE packages; --`, "needle OR other", "name:needle", "NEAR(needle other)", ""}
	random := rand.New(rand.NewPCG(42, 91))
	alphabet := []rune("abcЯёЕж中🙂 %_\"[]-:*")
	for range 240 {
		value := make([]rune, 30)
		for i := range value {
			value[i] = alphabet[random.IntN(len(alphabet))]
		}
		names = append(names, string(value))
	}
	tx, err := s.conn.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for i, name := range names {
		if _, err = tx.Exec("INSERT INTO catalog(id,account_id,app_id,name,delisted) VALUES(?,'fixture',?,?,0)", fmt.Sprint(i), i+1, name); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	queries := []string{"ё", "ЁЛ", "ёлка", "е", "Ел", "IPA", " ", `"`, `%_`, `a"b`, "[x]", "two words", "中", "中文", "🧭地", "İ", "ß", "K", "Σ", "e\u0301", "abcd", "bcde", "café", "missing", `' OR 1=1 --`, "\" OR \"", `x'); DROP TABLE packages; --`, "needle OR other", "name:needle", "NEAR(needle other)", "*", "-"}
	for range 160 {
		runes := []rune(names[random.IntN(len(names))])
		if len(runes) == 0 {
			continue
		}
		start := random.IntN(len(runes))
		queries = append(queries, string(runes[start:min(len(runes), start+1+random.IntN(7))]))
	}
	for _, query := range queries {
		want := queryStrings(t, s.conn, "SELECT id FROM catalog WHERE instr(fold(name),fold(?))>0 ORDER BY id", query)
		if utf8.RuneCountInString(query) < 3 {
			want = []string{}
		}
		if got := searchResults(t, s.conn, catalogSearch, query); !slices.Equal(got, want) {
			t.Fatalf("query %q: indexed %v; literal %v", query, got, want)
		}
	}
}

func TestSearchUpdatesAndRollbacks(t *testing.T) {
	s := testStore(t)
	if _, err := s.conn.Exec(`INSERT INTO accounts(id) VALUES('fixture');
 INSERT INTO sessions(id,scope,csrf) VALUES('fixture','local','fixture');
 INSERT INTO catalog(id,account_id,app_id,name,delisted) VALUES('same','fixture',1,'Catalog',0);
 INSERT INTO packages(id,account_id,app_id,name,created_at,size) VALUES('same','fixture',1,'Package','',1);
 INSERT INTO shares(id,creator_session_id,owner_id,owner_name,created_at) VALUES('same','fixture','fixture','Owner','')`); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		source searchSource
		name   string
	}{{catalogSearch, "catalog"}, {packageSearch, "package"}, {shareSearch, "owner"}} {
		for _, size := range []int{3, len(item.name)} {
			if got := searchResults(t, s.conn, item.source, item.name[:size]); !slices.Equal(got, []string{"same"}) {
				t.Fatal(item, size, got)
			}
		}
	}
	tx, err := s.conn.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec("UPDATE packages SET name='Replacement' WHERE id='same'"); err != nil {
		t.Fatal(err)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if got := searchResults(t, s.conn, packageSearch, "Package"); len(got) != 1 {
		t.Fatal("rollback lost old index", got)
	}
	if got := searchResults(t, s.conn, packageSearch, "Replacement"); len(got) != 0 {
		t.Fatal("rollback kept new index", got)
	}
	if _, err = s.conn.Exec("UPDATE packages SET id='renamed',name='Replacement' WHERE id='same'"); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{"rep", "Replacement"} {
		if got := searchResults(t, s.conn, packageSearch, query); !slices.Equal(got, []string{"renamed"}) {
			t.Fatal("rename lost index", query, got)
		}
	}
	if got := searchResults(t, s.conn, packageSearch, "Package"); len(got) != 0 {
		t.Fatal("rename kept old index", got)
	}
	if _, err = s.conn.Exec("DELETE FROM packages WHERE id='renamed'"); err != nil {
		t.Fatal(err)
	}
	if got := searchResults(t, s.conn, packageSearch, "rep"); len(got) != 0 {
		t.Fatal("delete kept trigram index", got)
	}
	if _, err = s.conn.Exec("INSERT INTO search_names(search_names,rank) VALUES('integrity-check',1)"); err != nil {
		t.Fatal(err)
	}
}

func TestSearchQueryPlans(t *testing.T) {
	s := testStore(t)
	for _, query := range []string{"я", "ёж", "Ёлка", "three words"} {
		text, args := searchIds(catalogSearch, query)
		assertQueryPlan(t, s.conn, text, args, "VIRTUAL TABLE INDEX", "SEARCH d USING INTEGER PRIMARY KEY")
	}
	scope := Scope{Owner: "fixture", Public: true}
	where, args := searchFilter(catalogSearch, "WHERE account_id=?", []any{scope.Owner}, "needle")
	assertQueryPlan(t, s.conn, "SELECT count(*) FROM catalog "+where, args, "catalog_owner_page (account_id=? AND id=?)", "VIRTUAL TABLE INDEX")
}

func TestSearchScopesAndBulkActions(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if _, err := s.conn.Exec(`INSERT INTO accounts(id) VALUES('owner'),('other');
 INSERT INTO sessions(id,scope,csrf) VALUES('fixture','local','fixture');
 INSERT INTO catalog(id,account_id,app_id,name,delisted)
 VALUES('one','owner',1,'Needle own',0),('two','other',2,'Needle other',0)`); err != nil {
		t.Fatal(err)
	}
	first, second := NewId(), NewId()
	for i, id := range []string{first, second} {
		owner := []string{"owner", "other"}[i]
		if _, err := s.conn.Exec("INSERT INTO packages(id,account_id,app_id,name,created_at,size) VALUES(?,?,?,'Needle','',1)", id, owner, i+1); err != nil {
			t.Fatal(err)
		}
		if err := s.CreateShare(ctx, NewShare{Id: id, SessionId: "fixture", Owner: owner, Name: owner, Packages: []string{id}, Limit: 8}); err != nil {
			t.Fatal(err)
		}
	}
	for _, item := range []struct {
		scope Scope
		count int
	}{
		{Scope{Owner: "owner", Public: true}, 1},
		{Scope{Owner: "owner", Public: true, SharedLibrary: true}, 2},
	} {
		q := Query{Sort: "name", Search: "needle", Page: paging.Page{Limit: 10}}
		page, err := s.Library(ctx, item.scope, q)
		if err != nil || page.Total != int64(item.count) || len(page.Items) != item.count {
			t.Fatalf("library scope: %+v, %v", page, err)
		}
		entries, err := s.LibrarySelection(ctx, item.scope, wire.Selection{Filter: &wire.Filter{Query: "needle"}}, true)
		if err != nil || len(entries) != item.count {
			t.Fatalf("library selection scope: %+v, %v", entries, err)
		}
		catalog, err := s.Catalog(ctx, item.scope, q)
		if err != nil || catalog.Total != 1 || len(catalog.Items) != 1 || catalog.Items[0].Id != "1" {
			t.Fatalf("catalog scope: %+v, %v", catalog, err)
		}
	}
	owner := Scope{Owner: "owner", Public: true}
	ids, err := s.SelectShares(ctx, owner, "Needle")
	if err != nil || !slices.Equal(ids, []string{first}) {
		t.Fatal("share selection leaks another owner", ids, err)
	}
	root := Scope{Public: true, Service: true}
	ids, err = s.SelectShares(ctx, root, "Needle")
	if err != nil || len(ids) != 2 {
		t.Fatal("root search misses another owner", ids, err)
	}
	if err = s.StopShares(ctx, owner, wire.Selection{Filter: &wire.Filter{Query: "Needle"}}); err != nil {
		t.Fatal(err)
	}
	ids, err = s.SelectShares(ctx, root, "Needle")
	if err != nil || !slices.Equal(ids, []string{second}) {
		t.Fatal("scoped stop removed another owner's share", ids, err)
	}
}

func BenchmarkIndexedSearch(b *testing.B) {
	s := testStore(b)
	if _, err := s.conn.Exec(`INSERT INTO accounts(id) VALUES('fixture');
 WITH RECURSIVE n(i) AS (VALUES(1) UNION ALL SELECT i+1 FROM n WHERE i<20000)
 INSERT INTO catalog(id,account_id,app_id,name,delisted)
 SELECT printf('%08d',i),'fixture',i,CASE WHEN i%1000=0 THEN 'Редкая 🧭 中文 needle' ELSE 'Common application '||i END,0 FROM n`); err != nil {
		b.Fatal(err)
	}
	for _, query := range []string{"🧭 中", "中文 n", "needle"} {
		for _, indexed := range []bool{false, true} {
			label := "scan"
			text, args := "SELECT id FROM catalog WHERE instr(fold(name),fold(?))>0", []any{query}
			if indexed {
				label = "indexed"
				text, args = searchIds(catalogSearch, query)
			}
			b.Run(fmt.Sprintf("%s/%s", query, label), func(b *testing.B) {
				for b.Loop() {
					if got := queryStrings(b, s.conn, text, args...); len(got) != 20 {
						b.Fatal(got)
					}
				}
			})
		}
	}
}
