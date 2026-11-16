package library

import (
	"context"
	"os"
	"strings"
	"sync"
	"time"

	"appleseum/backend/internal/db"
	"appleseum/backend/internal/fault"
	"appleseum/backend/internal/wire"
)

const (
	maxExportTickets = 16
	exportLifetime   = time.Hour
)

type ExportFile struct {
	Id, Name string
	Info     os.FileInfo
}

type ticket struct {
	owner   string
	ids     []string
	expires time.Time
}

// Exports are a browser session's download links: a link names files chosen
// earlier, so the browser can fetch them with a plain GET.
type Exports struct {
	mu      sync.Mutex
	tickets map[string]ticket
}

func (s *Service) exportFiles(ctx context.Context, scope db.Scope, input wire.Selection) ([]ExportFile, error) {
	entries, err := s.db.LibrarySelection(ctx, scope, input, true)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, fault.InvalidSelection
	}
	ids := make([]string, len(entries))
	for i, entry := range entries {
		if entry.Status != wire.Done {
			return nil, fault.ActionUnavailable
		}
		ids[i] = entry.Id
	}
	records, err := s.db.ExportRecords(ctx, ids)
	if err != nil {
		return nil, err
	}
	if len(records) != len(entries) {
		return nil, fault.NotFound
	}
	files := make([]ExportFile, len(records))
	names := map[string]bool{}
	for i, record := range records {
		name := ExportName(record.Name, record.Version, record.AppId)
		if names[name] {
			name = strings.TrimSuffix(name, ".ipa") + "_" + record.Id + ".ipa"
		}
		names[name] = true
		info, err := os.Stat(s.db.PackagePath(record.Id))
		if err != nil || !info.Mode().IsRegular() {
			return nil, fault.MissingPackage
		}
		files[i] = ExportFile{Id: record.Id, Name: name, Info: info}
	}
	return files, nil
}

// Prepare checks the selection and returns a link id for it.
func (s *Service) Prepare(ctx context.Context, exports *Exports, scope db.Scope, input wire.Selection) (string, error) {
	files, err := s.exportFiles(ctx, scope, input)
	if err != nil {
		return "", err
	}
	ids := make([]string, len(files))
	for i, file := range files {
		ids[i] = file.Id
	}
	now := time.Now()
	exports.mu.Lock()
	defer exports.mu.Unlock()
	if exports.tickets == nil {
		exports.tickets = make(map[string]ticket)
	}
	oldest := ""
	for id, t := range exports.tickets {
		if !now.Before(t.expires) {
			delete(exports.tickets, id)
		} else if oldest == "" || t.expires.Before(exports.tickets[oldest].expires) {
			oldest = id
		}
	}
	if len(exports.tickets) >= maxExportTickets {
		delete(exports.tickets, oldest)
	}
	id := db.NewId()
	exports.tickets[id] = ticket{owner: scope.Owner, ids: ids, expires: now.Add(exportLifetime)}
	return id, nil
}

// Open resolves a link to its files again. The files stay in place until release.
func (s *Service) Open(ctx context.Context, exports *Exports, scope db.Scope, id string) (files []ExportFile, release func(), err error) {
	exports.mu.Lock()
	t, ok := exports.tickets[id]
	exports.mu.Unlock()
	if !ok || scope.Public && scope.Owner != t.owner || !time.Now().Before(t.expires) {
		return nil, nil, fault.NotFound
	}
	s.files.RLock()
	if files, err = s.exportFiles(ctx, scope, wire.Selection{Ids: t.ids}); err != nil {
		s.files.RUnlock()
		return nil, nil, err
	}
	return files, s.files.RUnlock, nil
}
