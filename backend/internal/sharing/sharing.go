// Package sharing decides what a share holds: it starts and stops shares and
// lists the files an installation manifest offers.
package sharing

import (
	"context"
	"os"

	"github.com/hyperion-cs/appleseum/applekit/hosting"
	"github.com/hyperion-cs/appleseum/applekit/ipa"

	"appleseum/backend/internal/db"
	"appleseum/backend/internal/fault"
	"appleseum/backend/internal/wire"
)

const (
	maxSharesPerOwner = 8
)

// Owner is who a share belongs to; it outlives the browser session that started it.
type Owner struct{ Id, Name string }

// Start shares the selected published files under one link.
func Start(ctx context.Context, s *db.Db, scope db.Scope, owner Owner, sessionId string, input wire.Selection) (wire.Share, error) {
	entries, err := s.LibrarySelection(ctx, scope, input, true)
	if err != nil {
		return wire.Share{}, err
	}
	if len(entries) == 0 {
		return wire.Share{}, fault.InvalidSelection
	}
	ids := make([]string, len(entries))
	for i, entry := range entries {
		if entry.Status != wire.Done {
			return wire.Share{}, fault.ActionUnavailable
		}
		if _, err := os.Stat(s.PackagePath(entry.Id)); err != nil {
			return wire.Share{}, fault.MissingPackage
		}
		ids[i] = entry.Id
	}
	share := db.NewShare{Id: db.NewId(), SessionId: sessionId, Owner: owner.Id, Name: owner.Name, Packages: ids, Limit: maxSharesPerOwner}
	if err = s.CreateShare(ctx, share); err != nil {
		return wire.Share{}, err
	}
	return wire.Share{Id: share.Id, Count: len(ids)}, nil
}

// Manifest lists the share's apps with the paths of their files.
func Manifest(ctx context.Context, s *db.Db, shareId string) ([]hosting.App, []Package, error) {
	files, err := s.HostedFiles(ctx, shareId)
	if err != nil {
		return nil, nil, err
	}
	if len(files) == 0 {
		return nil, nil, fault.NotFound
	}
	apps := make([]hosting.App, len(files))
	packages := make([]Package, len(files))
	for i, file := range files {
		packages[i] = Package{Id: file.Id, Size: file.Size}
		apps[i] = hosting.App{Metadata: ipa.Metadata{Bundle: file.Bundle, Version: file.Version, Name: file.Name}, Path: FilePath(shareId, file.Id)}
	}
	return apps, packages, nil
}

func FilePath(shareId, fileId string) string { return "/packages/" + shareId + "/" + fileId + ".ipa" }

func ManifestPath(shareId string) string { return "/manifests/" + shareId + ".plist" }

// File returns the path and size of a file only while the share includes it.
func File(ctx context.Context, s *db.Db, shareId, fileId string) (string, int64, error) {
	size, err := s.HostedSize(ctx, shareId, fileId)
	if err != nil {
		return "", 0, err
	}
	return s.PackagePath(fileId), size, nil
}
