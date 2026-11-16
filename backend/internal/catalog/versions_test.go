package catalog

import (
	"math"
	"testing"

	"appleseum/backend/internal/db"
	"appleseum/backend/internal/paging"
	"appleseum/backend/internal/wire"
)

func TestVersionCursorBounds(t *testing.T) {
	revision := db.NewId()
	for _, position := range []float64{-1, 1.5, float64(math.MaxInt32) + 1} {
		input := wire.VersionPageRequest{Cursor: paging.Token(paging.Key{Id: "1", Value: position}), Revision: revision}
		if _, err := versionPage(input); err == nil {
			t.Fatalf("invalid version position accepted: %v", position)
		}
	}
	for _, id := range []string{"0", "x", ""} {
		input := wire.VersionPageRequest{Cursor: paging.Token(paging.Key{Id: id, Value: 1}), Revision: revision}
		if _, err := versionPage(input); err == nil {
			t.Fatalf("invalid version id accepted: %q", id)
		}
	}
	if _, err := versionPage(wire.VersionPageRequest{Cursor: paging.Token(paging.Key{Id: "1", Value: 1})}); err == nil {
		t.Fatal("cursor without revision accepted")
	}
	input := wire.VersionPageRequest{Cursor: paging.Token(paging.Key{Id: "1", Value: float64(math.MaxInt32)}), Revision: revision}
	if page, err := versionPage(input); err != nil || page.Limit != DefaultVersionLimit {
		t.Fatal(page, err)
	}
}
