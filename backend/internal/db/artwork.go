package db

import "context"

type Asset struct {
	App          int64
	Icon, Source string
}

func (d *Db) Assets(ctx context.Context, owner string) (map[int64]Asset, error) {
	assets, err := collect(ctx, d.conn, func(row scanner) (Asset, error) {
		var a Asset
		return a, row.Scan(&a.App, &a.Icon, &a.Source)
	}, "SELECT app_id,icon,source_url FROM app_assets WHERE account_id=?", owner)
	byApp := make(map[int64]Asset, len(assets))
	for _, asset := range assets {
		byApp[asset.App] = asset
	}
	return byApp, err
}

func (d *Db) AssetIcon(ctx context.Context, owner string, appId int64) (string, error) {
	return value[string](d.conn.QueryRowContext(ctx, "SELECT icon FROM app_assets WHERE account_id=? AND app_id=?", owner, appId))
}

// SaveAsset keeps App Db artwork over an icon extracted from an IPA.
func (d *Db) SaveAsset(ctx context.Context, owner string, appId int64, icon, source string) error {
	_, err := d.conn.ExecContext(ctx, "INSERT INTO app_assets(account_id,app_id,icon,source_url) VALUES(?,?,?,?) ON CONFLICT(account_id,app_id) DO UPDATE SET icon=excluded.icon,source_url=excluded.source_url WHERE excluded.source_url!='' OR app_assets.source_url=''", owner, appId, icon, source)
	return err
}

func (d *Db) CanReadIcon(ctx context.Context, scope Scope, icon string) (bool, error) {
	query := "SELECT 1 FROM app_assets a WHERE a.icon=? AND (a.account_id=?"
	if scope.shared() || scope.Service {
		query += " OR EXISTS(SELECT 1 FROM ready_packages f WHERE f.account_id=a.account_id AND f.app_id=a.app_id)"
	}
	return exists(ctx, d.conn, query+")", icon, scope.Owner)
}
