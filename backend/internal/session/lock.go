// Package session keeps browser sessions and their workspaces: who is signed
// in, which Apple operations run, and the download worker of each session.
package session

import "context"

// lock is a mutex whose waiters give up when their context ends.
type lock chan struct{}

func newLock() lock { return make(lock, 1) }

func (l lock) Lock(ctx context.Context) error {
	select {
	case l <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (l lock) TryLock() bool {
	select {
	case l <- struct{}{}:
		return true
	default:
		return false
	}
}

func (l lock) Unlock() { <-l }
