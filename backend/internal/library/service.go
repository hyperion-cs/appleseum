// Package library owns the files of the data directory: queued and running
// transfers, imported and downloaded IPAs, their exports and app icons.
package library

import (
	"context"
	"sync"

	"appleseum/backend/internal/db"
	"appleseum/backend/internal/fault"
	"appleseum/backend/internal/wire"
)

type Service struct {
	db     *db.Db
	report func(string, error)
	Icons  *Icons

	// Exports read files while deletion waits; publication only adds new files.
	files sync.RWMutex

	mu      sync.Mutex
	running map[string]context.CancelFunc
	public  bool
	slots   transferSlots
	storage storageGuard
}

func New(s *db.Db, fetchArtwork func(context.Context, string) ([]byte, error), report func(string, error), public bool) *Service {
	return &Service{
		db:      s,
		report:  report,
		Icons:   &Icons{db: s, fetch: fetchArtwork, report: report},
		running: make(map[string]context.CancelFunc),
		public:  public,
		storage: storageGuard{directory: s.Directory(), public: public, space: diskSpace},
	}
}

// Track registers a running transfer so Cancel can stop it; untrack when it ends.
func (s *Service) Track(id string, cancel context.CancelFunc) (untrack func()) {
	s.mu.Lock()
	s.running[id] = cancel
	s.mu.Unlock()
	return func() {
		s.mu.Lock()
		delete(s.running, id)
		s.mu.Unlock()
	}
}

// Running reports whether a transfer with this id is being processed now.
func (s *Service) Running(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running[id] != nil
}

// Cancel stops a transfer. One that is already running learns about it from
// its context and from its row, whichever it checks first.
func (s *Service) Cancel(ctx context.Context, scope db.Scope, id string) error {
	running, err := s.db.CancelDownload(ctx, scope, id)
	if err != nil || !running {
		return err
	}
	s.mu.Lock()
	cancel := s.running[id]
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return nil
}

// Delete removes the selected files and cancels selected transfers. Another
// owner's file fails an explicit selection and is skipped by a select-all.
func (s *Service) Delete(ctx context.Context, scope db.Scope, input wire.Selection) error {
	all := input.Filter != nil
	entries, err := s.db.LibrarySelection(ctx, scope, input, !all)
	if err != nil {
		return err
	}
	var ready, transfers []string
	for _, entry := range entries {
		switch {
		case !scope.Owns(entry.Owner) && all:
		case !scope.Owns(entry.Owner):
			return fault.ReadOnly
		case entry.Status == wire.Done:
			ready = append(ready, entry.Id)
		default:
			transfers = append(transfers, entry.Id)
		}
	}
	// An export streaming one of these files must not lose it midway.
	if !s.files.TryLock() {
		return fault.FilesInUse
	}
	defer s.files.Unlock()
	if err = s.db.DeletePackages(ctx, ready); err != nil {
		return err
	}
	for _, id := range transfers {
		if err = s.Cancel(ctx, scope, id); err != nil && !fault.Is(err, fault.ActionUnavailable) {
			return err
		}
	}
	return nil
}
