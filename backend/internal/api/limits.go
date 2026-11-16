package api

import (
	"sync"
	"time"
)

const (
	Read RateClass = iota + 1
	Login
	Action
	Debug
	Files
	LoginPeer
)

const (
	rateWindowDuration         = time.Minute
	readRequestsPerMinute      = 600
	loginRequestsPerMinute     = 10
	loginPeerRequestsPerMinute = 120
	actionRequestsPerMinute    = 120
	maxRateWindows             = 4_096
)

type RateClass uint8

func (class RateClass) Limit() int {
	switch class {
	case Read, Debug, Files:
		return readRequestsPerMinute
	case Login:
		return loginRequestsPerMinute
	case Action:
		return actionRequestsPerMinute
	case LoginPeer:
		return loginPeerRequestsPerMinute
	default:
		panic("invalid request rate class")
	}
}

type rateKey struct {
	class    RateClass
	identity string
}
type rateWindow struct {
	start time.Time
	count int
}

// Limiter is shared by every browser session of one server.
// Its zero value is ready to use.
type Limiter struct {
	mu      sync.Mutex
	windows map[rateKey]rateWindow
}

func (l *Limiter) Allow(class RateClass, identity string) bool {
	return l.allow(class, identity, time.Now())
}

func (l *Limiter) allow(class RateClass, identity string, now time.Time) bool {
	limit := class.Limit()
	key := rateKey{class, identity}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.windows == nil {
		l.windows = make(map[rateKey]rateWindow)
	}
	window, known := l.windows[key]
	if !known && len(l.windows) >= maxRateWindows {
		for key, item := range l.windows {
			if now.Sub(item.start) >= rateWindowDuration {
				delete(l.windows, key)
			}
		}
		if len(l.windows) >= maxRateWindows {
			return false
		}
	}
	if !known || now.Sub(window.start) >= rateWindowDuration {
		window = rateWindow{start: now}
	}
	if window.count >= limit {
		return false
	}
	window.count++
	l.windows[key] = window
	return true
}
