package library

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"appleseum/backend/internal/fault"
)

const (
	MaxTransferBytes int64 = 4_294_967_296
)

const (
	storageCheckInterval = 5 * time.Second
)

type storageGuard struct {
	mu         sync.Mutex
	directory  string
	public     bool
	space      func(string) (free, total int64, err error)
	reserved   int64
	blocked    int64
	failure    fault.Code
	retryAfter time.Time
}

func (g *storageGuard) capacity() (int64, int64, error) {
	free, total, err := g.space(g.directory)
	floor := int64(1 << 30)
	if g.public {
		floor = max(2<<30, total/20)
	}
	return free, floor, err
}

// Reservations include the source archive and its assembled copy. They are
// conservative until publication, so concurrent starts cannot spend the same space.
func (g *storageGuard) reserve(bytes int64) (func(), error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.failure != "" {
		return nil, g.failure
	}
	free, floor, err := g.capacity()
	if err != nil {
		return nil, err
	}
	if free < floor+g.reserved+bytes {
		g.blocked = max(g.blocked, bytes)
		return nil, fault.StorageFull
	}
	g.reserved += bytes
	return sync.OnceFunc(func() { g.mu.Lock(); g.reserved -= bytes; g.mu.Unlock() }), nil
}

func (g *storageGuard) check() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	free, floor, err := g.capacity()
	if err != nil {
		return err
	}
	if free < floor {
		g.blocked = max(g.blocked, 1)
		return fault.StorageFull
	}
	return nil
}

func (g *storageGuard) ready() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	free, floor, err := g.capacity()
	if err != nil {
		return err
	}
	if free < floor+g.reserved+g.blocked {
		return fault.StorageFull
	}
	if g.failure != "" {
		if time.Now().Before(g.retryAfter) {
			return g.failure
		}
		g.retryAfter = time.Now().Add(storageCheckInterval)
		if err := g.probe(); err != nil {
			g.failure = fault.Of(err)
			return g.failure
		}
		g.failure = ""
	}
	g.blocked = 0
	return nil
}

// A free-byte count cannot detect every quota or read-only filesystem. After
// an actual write failure, probe writing before allowing another queued job.
func (g *storageGuard) probe() (err error) {
	file, err := os.CreateTemp(filepath.Join(g.directory, "packages"), ".space-")
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, os.Remove(file.Name())) }()
	_, err = file.Write(make([]byte, 32_768))
	if closeErr := file.Close(); closeErr != nil {
		err = errors.Join(err, closeErr)
	}
	return err
}

func (s *Service) StorageFailure(err error) {
	if err == nil {
		return
	}
	code := fault.Of(err)
	var path *os.PathError
	var link *os.LinkError
	if code != fault.StorageFull && !errors.As(err, &path) && !errors.As(err, &link) && !errors.Is(err, syscall.EROFS) {
		return
	}
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	s.storage.mu.Lock()
	s.storage.blocked = max(s.storage.blocked, 1)
	s.storage.failure = code
	s.storage.retryAfter = time.Now().Add(storageCheckInterval)
	s.storage.mu.Unlock()
}

func (s *Service) StorageError() fault.Code {
	if err := s.storage.ready(); err != nil {
		return fault.Of(err)
	}
	return ""
}

func (s *Service) WaitStorage(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := s.storage.ready(); err == nil {
			return nil
		} else if fault.Of(err) != fault.StorageFull {
			return err
		}
		timer := time.NewTimer(storageCheckInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (s *Service) ReserveDownload(total int64) (func(), error) {
	if total < 0 {
		total = MaxTransferBytes
	}
	if total > MaxTransferBytes {
		return nil, fault.FileTooLarge
	}
	return s.storage.reserve(2 * (total + (8 << 20)))
}

func (s *Service) ReserveUpload(total int64) (func(), error) {
	if total < 0 || total > MaxTransferBytes {
		return nil, fault.FileTooLarge
	}
	return s.storage.reserve(total)
}

func (s *Service) CheckStorage() error { return s.storage.check() }

func (s *Service) RemoveTemporary(path string) {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		s.report("temporary_remove", err)
	}
}
