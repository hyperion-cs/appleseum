// Package catalog keeps the account's purchase list and the versions of each app.
package catalog

import (
	"cmp"
	"context"
	"errors"
	"math"
	"time"

	"github.com/hyperion-cs/appleseum/applekit"

	"appleseum/backend/internal/account"
	"appleseum/backend/internal/db"
	"appleseum/backend/internal/fault"
	"appleseum/backend/internal/library"
	"appleseum/backend/internal/paging"
	"appleseum/backend/internal/wire"
)

const (
	DefaultVersionLimit = 5
)

const (
	versionListLifetime = 15 * time.Minute
	maxVersionPosition  = math.MaxInt32
	fetchWeight         = 80.0
	artworkWeight       = 19.0
)

// RemoteError is a failure Apple reported, as opposed to one of local storage.
type RemoteError struct{ Err error }

func (e *RemoteError) Error() string { return e.Err.Error() }

func (e *RemoteError) Unwrap() error { return e.Err }

// Refresh replaces the saved purchase list with Apple's and caches its artwork.
func Refresh(ctx context.Context, s *db.Db, icons *library.Icons, call account.Call, owner, sessionId string, progress func(float64)) error {
	var apps []applekit.CatalogApp
	err := call(ctx, func(session account.Session) error {
		var err error
		apps, err = session.Catalog(ctx, func(value float64) { progress(value * fetchWeight / 100) })
		return err
	})
	if err != nil {
		return &RemoteError{err}
	}
	icons.FromCatalog(ctx, owner, apps, func(value float64) { progress(fetchWeight + value*artworkWeight/100) })
	return s.ReplaceCatalog(ctx, owner, sessionId, apps)
}

func versionPage(input wire.VersionPageRequest) (paging.Page, error) {
	page, err := paging.NewPage(cmp.Or(input.Limit, DefaultVersionLimit), input.Cursor, input.Move)
	if err != nil || page.After == nil {
		return page, err
	}
	position, numeric := page.After.Value.(float64)
	if !numeric || position < 0 || position > maxVersionPosition || position != math.Trunc(position) || !wire.Number(page.After.Id) || input.Revision == "" {
		return page, fault.InvalidRequest
	}
	return page, nil
}

// authentication reports whether Apple refused the session rather than one version.
func authentication(err error) bool {
	var code fault.Code
	return errors.As(err, &code) || errors.Is(err, applekit.ErrSessionExpired)
}

// Versions reads one page of an app's versions and asks Apple only for versions it has not described yet.
func Versions(ctx context.Context, s *db.Db, call account.Call, owner string, appId int64, input wire.VersionPageRequest, progress func(int)) (wire.VersionPage, error) {
	page, err := versionPage(input)
	if err != nil {
		return wire.VersionPage{}, err
	}
	name, err := s.CatalogName(ctx, owner, appId)
	if err != nil {
		return wire.VersionPage{}, err
	}
	list, err := s.VersionList(ctx, owner, appId)
	if err != nil {
		return wire.VersionPage{}, err
	}
	if page.After != nil && input.Revision != list.Revision {
		return wire.VersionPage{}, fault.VersionsChanged
	}
	if page.After == nil && (list.Revision == "" || time.Since(list.FetchedAt) > versionListLifetime) {
		var ids []string
		var latest string
		if err = call(ctx, func(session account.Session) error { ids, latest, err = session.Versions(ctx, appId); return err }); err != nil {
			return wire.VersionPage{}, &RemoteError{err}
		}
		if list, err = s.SaveVersionList(ctx, owner, appId, latest, ids); err != nil {
			return wire.VersionPage{}, err
		}
	}
	result, err := s.Versions(ctx, owner, appId, list, page)
	if err != nil {
		return result, err
	}
	result.Name = name
	if len(result.Items) == 0 && page.After != nil {
		return wire.VersionPage{}, fault.InvalidRequest
	}
	total, completed := float64(100*len(result.Items)), 0.0
	report := func(units float64) {
		if total > 0 {
			progress(int(units * 100 / total))
		}
	}
	for _, item := range result.Items {
		if item.Version != "" {
			completed += 100
		}
	}
	report(completed)
	for i := range result.Items {
		item := &result.Items[i]
		if item.Version != "" {
			continue
		}
		var metadata applekit.VersionMetadata
		err = call(ctx, func(session account.Session) error {
			metadata, err = session.VersionInfo(ctx, appId, item.Id, func(percent float64) { report(completed + min(100, max(0, percent))) })
			return err
		})
		completed += 100
		report(completed)
		switch {
		case ctx.Err() != nil:
			return wire.VersionPage{}, ctx.Err()
		case err != nil && authentication(err):
			return wire.VersionPage{}, &RemoteError{err}
		case err != nil || metadata.Version == "":
			item.Error = fault.VersionUnavailable
			continue
		}
		item.Version, item.MinimumOs = metadata.Version, metadata.MinimumOs
		if err = s.SaveVersionMetadata(ctx, owner, appId, item.Id, metadata); err != nil {
			return wire.VersionPage{}, err
		}
	}
	return result, nil
}
