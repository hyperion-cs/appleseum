package db

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"time"

	"github.com/hyperion-cs/appleseum/applekit"

	"appleseum/backend/internal/paging"
	"appleseum/backend/internal/wire"
)

// VersionList is the saved order of an app's versions; cursors are valid only within its revision.
type VersionList struct {
	Revision, Latest string
	FetchedAt        time.Time
}

func (d *Db) VersionList(ctx context.Context, owner string, appId int64) (VersionList, error) {
	var list VersionList
	var fetched string
	err := d.conn.QueryRowContext(ctx, "SELECT revision,fetched_at,latest FROM app_version_lists WHERE account_id=? AND app_id=?", owner, appId).Scan(&list.Revision, &fetched, &list.Latest)
	if errors.Is(err, sql.ErrNoRows) {
		return list, nil
	}
	list.FetchedAt = parseTime(fetched)
	return list, err
}

// SaveVersionList stores a new revision: the latest version first, then the rest newest first.
// Versions no longer listed keep their metadata but leave the order.
func (d *Db) SaveVersionList(ctx context.Context, owner string, appId int64, latest string, ids []string) (VersionList, error) {
	list := VersionList{Revision: NewId(), Latest: latest, FetchedAt: time.Now()}
	if !wire.Number(latest) {
		return list, errors.New("invalid latest version ID")
	}
	ordered := jsonArray[string]{latest}
	for _, id := range slices.Backward(ids) {
		if wire.Number(id) && !slices.Contains(ordered, id) {
			ordered = append(ordered, id)
		}
	}
	return list, d.tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "INSERT INTO app_version_lists(account_id,app_id,revision,fetched_at,latest) VALUES(?,?,?,?,?) ON CONFLICT(account_id,app_id) DO UPDATE SET revision=excluded.revision,fetched_at=excluded.fetched_at,latest=excluded.latest", owner, appId, list.Revision, timeText(list.FetchedAt), latest); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE app_versions SET position=-1 WHERE account_id=? AND app_id=?", owner, appId); err != nil {
			return err
		}
		// WHERE true disambiguates SQLite's SELECT ... ON CONFLICT syntax.
		_, err := tx.ExecContext(ctx, `INSERT INTO app_versions(account_id,app_id,version_id,position)
 SELECT ?,?,value,key FROM json_each(?) WHERE true
 ON CONFLICT(account_id,app_id,version_id) DO UPDATE SET position=excluded.position`, owner, appId, ordered)
		return err
	})
}

func (d *Db) Versions(ctx context.Context, owner string, appId int64, list VersionList, page paging.Page) (wire.VersionPage, error) {
	result := wire.VersionPage{Revision: list.Revision, Items: []wire.AppVersion{}}
	if err := d.conn.QueryRowContext(ctx, "SELECT count(*) FROM app_versions WHERE account_id=? AND app_id=? AND position>=0", owner, appId).Scan(&result.Total); err != nil {
		return result, err
	}
	sql, args := versionPageSql(owner, appId, page)
	items, err := collect(ctx, d.conn, func(row scanner) (wire.AppVersion, error) {
		var item wire.AppVersion
		var position int
		err := row.Scan(&position, &item.Id, &item.Version, &item.MinimumOs)
		item.Latest = item.Id == list.Latest
		item.Cursor = paging.Token(paging.Key{Id: item.Id, Value: position})
		return item, err
	}, sql, args...)
	if err != nil {
		return result, err
	}
	result.Items, result.Next, result.Previous = items, len(items) > page.Limit, page.After != nil
	if result.Next {
		result.Items = items[:page.Limit]
	}
	if page.Back {
		slices.Reverse(result.Items)
		result.Next, result.Previous = result.Previous, result.Next
	}
	return result, nil
}

func versionPageSql(owner string, appId int64, page paging.Page) (string, []any) {
	where, args := "account_id=? AND app_id=? AND position>=0", []any{owner, appId}
	order, op := "ASC", ">"
	if page.Back {
		order, op = "DESC", "<"
	}
	if page.After != nil {
		where += " AND (position,version_id)" + op + "(?,?)"
		args = append(args, page.After.Value, page.After.Id)
	}
	return "SELECT position,version_id,version,minimum_os FROM app_versions WHERE " + where + " ORDER BY position " + order + ",version_id " + order + " LIMIT ?", append(args, page.Limit+1)
}

func (d *Db) SaveVersionMetadata(ctx context.Context, owner string, appId int64, id string, metadata applekit.VersionMetadata) error {
	_, err := d.conn.ExecContext(ctx, "UPDATE app_versions SET version=?,build=?,minimum_os=? WHERE account_id=? AND app_id=? AND version_id=?", metadata.Version, metadata.Build, metadata.MinimumOs, owner, appId, id)
	return err
}
