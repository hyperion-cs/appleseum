package dns

import (
	"context"
	"time"
)

// Event contains DNS metadata only; it never includes HTTP headers or bodies.
// System resolution reports the server when the platform exposes it in an error.
type Event struct {
	Kind        string // failure, fallback, or selected
	Host        string
	Strategy    string
	Previous    string
	Provider    string
	Server      string
	Network     string
	Duration    time.Duration
	Err         error
	VpnPossible bool
}

type observerKey struct{}

func WithObserver(ctx context.Context, observe func(Event)) context.Context {
	return context.WithValue(ctx, observerKey{}, observe)
}

func emit(ctx context.Context, event Event) {
	if observe, _ := ctx.Value(observerKey{}).(func(Event)); observe != nil {
		observe(event)
	}
}
