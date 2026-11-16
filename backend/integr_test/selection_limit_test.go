package integration_test

import (
	"testing"

	"appleseum/backend/internal/wire"
)

func TestMassLibraryActionsRejectOverLimitWithoutTruncating(t *testing.T) {
	dir := t.TempDir()
	h := launch(t, dir, nil)
	defer h.close()
	if _, err := deviceDb(t, dir).Exec(`INSERT OR IGNORE INTO accounts(id) VALUES('');
 WITH RECURSIVE n(value) AS (SELECT 1 UNION ALL SELECT value+1 FROM n WHERE value<=512)
 INSERT INTO packages(id,account_id,app_id,name,bundle,version,size,created_at)
 SELECT printf('00000000-0000-4000-8000-%012d',value),'',-value,'Fixture','test.fixture','1',1,'2026-11-10' FROM n`); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		path string
		body any
	}{
		{"/api/library/selection", wire.Selection{Filter: &wire.Filter{}}},
		{"/api/shares", wire.Selection{Filter: &wire.Filter{}}},
		{"/api/library/export", wire.Selection{Filter: &wire.Filter{}}},
	} {
		var result wire.Error
		h.request(t, "POST", c.path, c.body, 400, &result)
		if result.Code != "selection_too_large" {
			t.Fatalf("%s: %s", c.path, result.Code)
		}
	}
	var count int
	if err := deviceDb(t, dir).QueryRow("SELECT count(*) FROM shares").Scan(&count); err != nil || count != 0 {
		t.Fatal("rejected action created a share", count, err)
	}
}
