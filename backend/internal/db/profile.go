package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/hyperion-cs/appleseum/applekit"

	"appleseum/backend/internal/fault"
)

// ProfileCache is the last profile Apple returned and the outcome of the last attempt.
type ProfileCache struct {
	Data      *applekit.AccountProfile
	Error     fault.Code
	Attempted time.Time
}

func (d *Db) Profile(ctx context.Context, owner string) (ProfileCache, error) {
	var cache ProfileCache
	var raw []byte
	var attempted string
	err := d.conn.QueryRowContext(ctx, "SELECT profile,profile_error,profile_attempted_at FROM accounts WHERE id=?", owner).Scan(&raw, &cache.Error, &attempted)
	if errors.Is(err, sql.ErrNoRows) {
		return cache, nil
	}
	cache.Attempted = parseTime(attempted)
	if err == nil && len(raw) > 0 {
		err = json.Unmarshal(raw, &cache.Data)
	}
	return cache, err
}

func (d *Db) ClearProfile(ctx context.Context, owner string) error {
	_, err := d.conn.ExecContext(ctx, "UPDATE accounts SET profile=NULL,profile_error='',profile_attempted_at='' WHERE id=?", owner)
	return err
}

// SaveProfile stores the outcome of an attempt made now.
func (d *Db) SaveProfile(ctx context.Context, owner string, cache ProfileCache) error {
	var raw []byte
	if cache.Data != nil {
		var err error
		if raw, err = json.Marshal(cache.Data); err != nil {
			return err
		}
	}
	_, err := d.conn.ExecContext(ctx, "UPDATE accounts SET profile=?,profile_error=?,profile_attempted_at=? WHERE id=?", raw, cache.Error, now(), owner)
	return err
}
