package db

import (
	"context"
	"testing"

	"appleseum/backend/internal/wire"
	"github.com/hyperion-cs/appleseum/applekit"
)

func TestCatalogUpdatesAndQueueRespectVersionAndVisibility(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		scope                 Scope
		owner, display, build string
		update, saved         bool
	}{
		{"old build", Scope{Owner: "owner"}, "owner", "1.0", "1", true, true},
		{"same release", Scope{Owner: "owner"}, "owner", "1.0", "2", false, true},
		{"shared old release", Scope{Owner: "owner", Public: true, SharedLibrary: true}, "other", "0.9", "1", true, true},
		{"shared current release", Scope{Owner: "owner"}, "other", "1.0", "2", false, true},
		{"private other account", Scope{Owner: "owner", Public: true}, "other", "1.0", "2", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := testStore(t)
			ctx := context.Background()
			if _, err := s.conn.Exec(`INSERT INTO accounts(id) VALUES('owner'),('other'); INSERT INTO sessions(id,scope,csrf) VALUES('local','local','csrf');
    INSERT INTO packages(id,account_id,app_id,name,created_at,display_version,version,size) VALUES('saved',?,1,'App','',?,?,10)`, tc.owner, tc.display, tc.build); err != nil {
				t.Fatal(err)
			}
			if err := s.ReplaceCatalog(ctx, "owner", "local", []applekit.CatalogApp{{Id: 1, Name: "App"}}); err != nil {
				t.Fatal(err)
			}
			if _, err := s.SaveVersionList(ctx, "owner", 1, "102", nil); err != nil {
				t.Fatal(err)
			}
			if err := s.SaveVersionMetadata(ctx, "owner", 1, "102", applekit.VersionMetadata{Version: "1.0", Build: "2"}); err != nil {
				t.Fatal(err)
			}
			result, err := s.Catalog(ctx, tc.scope, page(t, "name", 10, nil))
			if err != nil {
				t.Fatal(err)
			}
			item := result.Items[0]
			if item.InLibrary != tc.saved || item.UpdateAvailable != tc.update {
				t.Fatalf("unexpected catalog: %+v", item)
			}
			ids, err := s.CatalogSelection(ctx, tc.scope, wire.Selection{Ids: []string{"1"}}, true)
			if err != nil {
				t.Fatal(err)
			}
			eligible := tc.update || !tc.saved
			if (len(ids) == 1) != eligible {
				t.Fatalf("selection %v; eligible %v", ids, eligible)
			}
			if err := s.EnqueueDownloads(ctx, tc.scope, "local", []int64{1}, nil); err != nil {
				t.Fatal(err)
			}
			job, ok, err := s.ClaimDownload(ctx, tc.scope, "local")
			if err != nil || ok != eligible {
				t.Fatalf("claim %+v %v %v", job, ok, err)
			}
		})
	}
}
