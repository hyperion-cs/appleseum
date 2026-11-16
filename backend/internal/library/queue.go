package library

import (
	"context"
	"slices"
	"strconv"

	"appleseum/backend/internal/db"
	"appleseum/backend/internal/fault"
	"appleseum/backend/internal/wire"
)

// Queue adds the selected apps to the session's download queue. Explicit ids
// may name a saved app to download another version; select-all skips them.
func (s *Service) Queue(ctx context.Context, scope db.Scope, sessionId string, request wire.DownloadRequest) error {
	input := request.Selection
	all := input.Filter != nil
	apps, err := s.db.CatalogSelection(ctx, scope, input, all)
	if err != nil {
		return err
	}
	if len(apps) == 0 || !all && len(apps) != len(input.Ids) {
		return fault.NotFound
	}
	versions := make(map[int64]string, len(request.Versions))
	for _, choice := range request.Versions {
		appId, _ := strconv.ParseInt(choice.AppId, 10, 64)
		if !slices.Contains(apps, appId) || versions[appId] != "" {
			return fault.InvalidRequest
		}
		available, err := s.db.VersionAvailable(ctx, scope.Owner, appId, choice.VersionId)
		if err != nil {
			return err
		}
		if !available {
			return fault.InvalidRequest
		}
		versions[appId] = choice.VersionId
	}
	return s.db.EnqueueDownloads(ctx, scope, sessionId, apps, versions)
}
