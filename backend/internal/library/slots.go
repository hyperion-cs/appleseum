package library

import (
	"context"
	"sync"

	"appleseum/backend/internal/fault"
)

const (
	AccountTransfers = 3
)

type slotWaiter struct {
	owner   string
	upload  bool
	ready   chan struct{}
	granted bool
}

// FIFO among eligible requests: an account at its limit cannot block others.
// A slot covers licensing, transfer and IPA assembly, including imports.
type transferSlots struct {
	mu              sync.Mutex
	active, uploads int
	accounts        map[string]int
	importing       map[string]int
	waiting         []*slotWaiter
}

func (s *transferSlots) acquire(ctx context.Context, owner string, public, upload, wait bool) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	if s.accounts == nil {
		s.accounts = make(map[string]int)
		s.importing = make(map[string]int)
	}
	w := &slotWaiter{owner: owner, upload: upload, ready: make(chan struct{})}
	s.waiting = append(s.waiting, w)
	s.dispatch(public)
	if !wait && !w.granted {
		s.remove(w)
		s.mu.Unlock()
		return nil, fault.ImportBusy
	}
	s.mu.Unlock()
	release := sync.OnceFunc(func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.active--
		s.accounts[owner]--
		if s.accounts[owner] == 0 {
			delete(s.accounts, owner)
		}
		if upload {
			s.uploads--
			s.importing[owner]--
			if s.importing[owner] == 0 {
				delete(s.importing, owner)
			}
		}
		s.dispatch(public)
	})
	select {
	case <-w.ready:
		if err := ctx.Err(); err != nil {
			release()
			return nil, err
		}
		return release, nil
	case <-ctx.Done():
		s.mu.Lock()
		granted := w.granted
		if !granted {
			s.remove(w)
			s.dispatch(public)
		}
		s.mu.Unlock()
		if granted {
			release()
		}
		return nil, ctx.Err()
	}
}

func (s *transferSlots) remove(w *slotWaiter) {
	for i, p := range s.waiting {
		if p == w {
			s.waiting = append(s.waiting[:i], s.waiting[i+1:]...)
			return
		}
	}
}

func (s *transferSlots) dispatch(public bool) {
	limit := 3
	if public {
		limit = 16
	}
	for i := 0; i < len(s.waiting) && s.active < limit; {
		w := s.waiting[i]
		if s.accounts[w.owner] >= AccountTransfers || w.upload && (s.uploads >= 2 || public && s.importing[w.owner] > 0) {
			i++
			continue
		}
		s.active++
		s.accounts[w.owner]++
		if w.upload {
			s.uploads++
			s.importing[w.owner]++
		}
		w.granted = true
		s.remove(w)
		close(w.ready)
	}
}

func (s *Service) DownloadSlot(ctx context.Context, owner string) (func(), error) {
	return s.slots.acquire(ctx, owner, s.public, false, true)
}
