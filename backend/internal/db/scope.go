package db

import "appleseum/backend/internal/wire"

// Scope is what one requester may see: their own rows, or more in local,
// shared-library and service modes. Lists and actions apply the same rules.
type Scope struct {
	Owner                          string
	Public, Service, SharedLibrary bool
}

// shared reports whether published files of every owner are visible.
func (s Scope) shared() bool { return !s.Public || s.SharedLibrary }

// ManagesAll reports whether files of every owner may be changed.
func (s Scope) ManagesAll() bool { return !s.Public || s.Service }

// Owns reports whether a row of this owner may be changed.
func (s Scope) Owns(owner string) bool { return s.ManagesAll() || owner == s.Owner }

func (s Scope) libraryFilter(mine bool) (string, []any) {
	switch {
	case s.Service:
		return "WHERE 1=1", nil
	case !s.shared():
		return "WHERE account_id=?", []any{s.Owner}
	case mine && s.Public:
		return "WHERE account_id=?", []any{s.Owner}
	}
	return "WHERE (account_id=? OR status=?)", []any{s.Owner, wire.Done}
}

func (s Scope) catalogPackages() string {
	query := "FROM ready_packages f WHERE f.app_id=catalog.app_id"
	if !s.shared() {
		query += " AND f.account_id=catalog.account_id"
	}
	return query
}

// catalogSize is the newest visible IPA size, so display, sorting and cursors agree.
func (s Scope) catalogSize() string {
	return "COALESCE((SELECT f.size " + s.catalogPackages() + " ORDER BY f.id DESC LIMIT 1),catalog.size)"
}

// catalogDownloadable: visible ready files prevent another download; active jobs belong to their owner.
func (s Scope) catalogDownloadable() string {
	return "NOT EXISTS(SELECT 1 " + s.catalogPackages() + ")" +
		" AND NOT EXISTS(SELECT 1 FROM transfers j WHERE j.account_id=catalog.account_id AND j.app_id=catalog.app_id AND j.status IN " + activeStatuses + ")"
}

func (s Scope) shareFilter(query string) (string, []any) {
	where, args := "WHERE 1=1", []any{}
	if s.Public && !s.Service {
		where, args = "WHERE owner_id=?", []any{s.Owner}
	}
	if query != "" {
		owners, ownerArgs := searchIds(shareSearch, query)
		packages, packageArgs := searchIds(packageSearch, query)
		where += " AND (id IN (" + owners + ") OR id IN (SELECT share_id FROM share_packages WHERE package_id IN (" + packages + ")))"
		args = append(append(args, ownerArgs...), packageArgs...)
	}
	return where, args
}
