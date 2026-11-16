package api

import (
	"net/url"
	"slices"

	"appleseum/backend/internal/db"
	"appleseum/backend/internal/fault"
	"appleseum/backend/internal/paging"
	"appleseum/backend/internal/wire"
)

const (
	defaultPageLimit = 25
)

// The newest rows come first unless the list is asked for by name.
var defaultSorts = map[wire.Table]string{wire.CatalogTable: "purchasedAt", wire.SharesTable: "createdAt"}

func oneOf(value string, allowed ...string) bool { return slices.Contains(allowed, value) }

// listQuery reads the paging, sorting, search and filter parameters of a list.
// It accepts only known parameters, each once; db checks the sort key itself.
func listQuery(q url.Values, table wire.Table) (db.Query, error) {
	for key, values := range q {
		if !oneOf(key, "limit", "cursor", "move", "sort", "direction", "q", "delisted", "mine") || len(values) != 1 {
			return db.Query{}, fault.InvalidRequest
		}
	}
	direction, delisted, mine := q.Get("direction"), q.Get("delisted"), q.Get("mine")
	_, filtersMine := q["mine"]
	if !oneOf(direction, "", "asc", "desc") || !oneOf(delisted, "", "true", "false") || !wire.Search(q.Get("q")) ||
		filtersMine && (table != wire.LibraryTable || !oneOf(mine, "true", "false")) {
		return db.Query{}, fault.InvalidRequest
	}
	query := db.Query{Sort: q.Get("sort"), Desc: direction == "desc", Search: q.Get("q"), Delisted: delisted == "true", Mine: mine != "false"}
	if query.Sort == "" {
		if sort, newestFirst := defaultSorts[table]; newestFirst {
			query.Sort, query.Desc = sort, direction != "asc"
		} else {
			query.Sort = "name"
		}
	}
	var err error
	query.Page, err = paging.ParsePage(q.Get("limit"), q.Get("cursor"), q.Get("move"), defaultPageLimit)
	return query, err
}
