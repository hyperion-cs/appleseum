package db

import (
	"context"
	"errors"
	"testing"
	"time"

	"appleseum/backend/internal/fault"
	"appleseum/backend/internal/paging"
	"appleseum/backend/internal/wire"
)

func TestSqlStructureRejectsInjectedInput(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	attacks := []string{`1 OR 1=1`, `name; DROP TABLE packages; --`, `" UNION SELECT sql FROM sqlite_schema --`, `name/*comment*/`}
	for _, scope := range []Scope{{}, {Public: true}, {Public: true, Service: true}, {Public: true, SharedLibrary: true}} {
		for _, attack := range attacks {
			q := Query{Sort: attack, Page: paging.Page{Limit: 1}}
			_, catalog := s.Catalog(ctx, scope, q)
			_, library := s.Library(ctx, scope, q)
			_, shares := s.Shares(ctx, scope, q)
			for _, err := range []error{catalog, library, shares} {
				if !errors.Is(err, fault.InvalidRequest) {
					t.Fatalf("sort %q accepted in scope %+v: %v", attack, scope, err)
				}
			}
			for _, table := range wire.TableNames {
				if scope.Sortable(table, attack) {
					t.Fatalf("%s sortable by %q", table, attack)
				}
			}
		}
	}
}

func TestSqlCursorCannotEscapeAccountScope(t *testing.T) {
	const attack = `' OR 1=1 --`

	s := testStore(t)
	ctx := context.Background()
	ids := []string{NewId(), NewId()}
	shares := []string{NewId(), NewId()}
	for i, owner := range []string{"owner", "other"} {
		if _, err := s.conn.Exec("INSERT INTO accounts(id) VALUES(?)", owner); err != nil {
			t.Fatal(err)
		}
		if _, err := s.conn.Exec("INSERT INTO sessions(id,scope,csrf) VALUES(?,'public',?)", owner, owner); err != nil {
			t.Fatal(err)
		}
		if _, err := s.conn.Exec("INSERT INTO catalog(id,account_id,app_id,name,delisted) VALUES(?,?,?,?,0)", ids[i], owner, i+1, attack); err != nil {
			t.Fatal(err)
		}
		if _, err := s.conn.Exec("INSERT INTO packages(id,account_id,app_id,name,created_at,size) VALUES(?,?,?,?,'',1)", ids[i], owner, i+1, attack); err != nil {
			t.Fatal(err)
		}
		if _, err := s.conn.Exec("INSERT INTO downloads(package_id,session_id,status) VALUES(?,?,'done')", ids[i], owner); err != nil {
			t.Fatal(err)
		}
		if err := s.CreateShare(ctx, NewShare{Id: shares[i], SessionId: owner, Owner: owner, Name: attack, Packages: []string{ids[i]}, Limit: 8}); err != nil {
			t.Fatal(err)
		}
	}
	scope := Scope{Owner: "owner", Public: true}
	key := paging.Key{Id: NewId(), Value: attack}
	for _, back := range []bool{false, true} {
		for _, resource := range []string{"catalog", "library", "shares"} {
			p := Query{Sort: "name", Page: paging.Page{Limit: 1, After: &key, Back: back}}
			switch resource {
			case "catalog":
				page, err := s.Catalog(ctx, scope, p)
				if err != nil || page.Total != 1 || len(page.Items) != 1 || page.Items[0].Id != "1" {
					t.Fatalf("catalog cursor escaped scope: %+v, %v", page, err)
				}
			case "library":
				page, err := s.Library(ctx, scope, p)
				if err != nil || page.Total != 1 || len(page.Items) != 1 || page.Items[0].Id != ids[0] {
					t.Fatalf("library cursor escaped scope: %+v, %v", page, err)
				}
			case "shares":
				page, err := s.Shares(ctx, scope, p)
				if err != nil || page.Total != 1 || len(page.Items) != 1 || page.Items[0].Id != shares[0] {
					t.Fatalf("share cursor escaped scope: %+v, %v", page, err)
				}
			}
		}
	}
}

func TestSqlSavedSettingsAreNeverSqlFragments(t *testing.T) {
	const attack = `name; DROP TABLE packages; --`

	s := testStore(t)
	ctx := context.Background()
	if err := s.CreateBrowserSession(ctx, attack, attack, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := s.EnsurePreferences(ctx, attack); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SavePreferences(ctx, attack, wire.PreferencesInput{Language: attack, Theme: attack}); err == nil {
		t.Fatal("invalid preference values bypassed constraints")
	}
	if preferences, err := s.Preferences(ctx, attack); err != nil || preferences.Language != "en" || preferences.Theme != "dark" {
		t.Fatal("rejected input changed preferences", preferences, err)
	}
	if _, err := s.SaveLibraryView(ctx, attack, wire.LibraryView{Sort: attack, Direction: "asc"}); err != nil {
		t.Fatal(err)
	}
	tables, err := s.Tables(ctx, attack)
	saved := tables.Library.Value
	if err != nil || saved.Sort != attack || saved.Direction != "asc" {
		t.Fatal("saved sorting did not remain literal", saved, err)
	}
	if _, err := s.Library(ctx, Scope{}, Query{Sort: saved.Sort, Page: paging.Page{Limit: 1}}); err == nil {
		t.Fatal("persisted SQL was accepted as a sorting expression")
	}
	var count int
	if err := s.conn.QueryRow("SELECT count(*) FROM packages").Scan(&count); err != nil || count != 0 {
		t.Fatalf("stored input damaged the schema: %d, %v", count, err)
	}
}
