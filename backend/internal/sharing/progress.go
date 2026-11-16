package sharing

import (
	"maps"
	"slices"
	"sync"
	"time"

	"appleseum/backend/internal/wire"
)

const (
	progressLifetime  = time.Minute
	maxAttempts       = 1_024
	maxIntervals      = 4_096
	interruptionDelay = 3 * time.Second
	// iOS never requests an app it already has. After this long without any
	// manifest or IPA activity, packages the device has not asked for count as skipped.
	settleDelay = 15 * time.Second
)

// Package supplies the full denominator before any IPA requests arrive.
type Package struct {
	Id   string
	Size int64
}

// Progress keeps what the server saw of recent installation attempts: one per
// share and trace id that the install link carried. An attempt is forgotten a
// minute after its last request ends; reading it does not prolong it.
type Progress struct {
	mu       sync.Mutex
	attempts map[attemptKey]*attempt
}

type attemptKey struct{ share, trace string }

type attempt struct {
	files                             map[string]*fileProgress
	manifests                         int // manifest responses in flight
	manifestRequested, manifestFailed bool
	last                              time.Time
}

type fileProgress struct {
	size                int64
	spans               []interval // delivered byte ranges, sorted and disjoint
	active              int
	requested, complete bool
	last                time.Time
}

type interval struct{ start, end int64 }

// ValidTrace reports whether value is a trace id: seven lowercase letters or digits.
func ValidTrace(value string) bool {
	if len(value) != 7 {
		return false
	}
	for _, c := range value {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

func (a *attempt) active() bool {
	if a.manifests > 0 {
		return true
	}
	for _, f := range a.files {
		if f.active > 0 {
			return true
		}
	}
	return false
}

func (a *attempt) expired(now time.Time) bool {
	return !a.active() && now.Sub(a.last) >= progressLifetime
}

// find returns the live attempt, dropping it once it has expired.
func (p *Progress) find(key attemptKey, now time.Time) *attempt {
	a := p.attempts[key]
	if a != nil && a.expired(now) {
		delete(p.attempts, key)
		return nil
	}
	return a
}

// open returns the attempt, starting it if there is room; nil means it is not tracked.
func (p *Progress) open(key attemptKey, now time.Time) *attempt {
	if a := p.find(key, now); a != nil {
		a.last = now
		return a
	}
	if len(p.attempts) >= maxAttempts {
		maps.DeleteFunc(p.attempts, func(_ attemptKey, a *attempt) bool { return a.expired(now) })
		if len(p.attempts) >= maxAttempts {
			return nil
		}
	}
	if p.attempts == nil {
		p.attempts = make(map[attemptKey]*attempt)
	}
	a := &attempt{files: make(map[string]*fileProgress), last: now}
	p.attempts[key] = a
	return a
}

func (a *attempt) file(id string, size int64) *fileProgress {
	f := a.files[id]
	if f == nil {
		f = &fileProgress{size: size}
		a.files[id] = f
	}
	return f
}

// Manifest observes one manifest GET and returns the function that ends it.
// Every package of the share joins the attempt before any of it is requested.
func (p *Progress) Manifest(share, trace string, files []Package) func(success bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	a := p.open(attemptKey{share, trace}, now)
	if a == nil {
		return func(bool) {}
	}
	for _, file := range files {
		a.file(file.Id, file.Size)
	}
	a.manifests++
	a.manifestRequested, a.manifestFailed = true, false
	return func(success bool) {
		p.mu.Lock()
		defer p.mu.Unlock()
		a.manifests--
		a.manifestFailed = !success
		a.last = time.Now()
	}
}

// Transfer observes one IPA GET. Parallel requests for the same file share its
// coverage, while each ends on its own.
type Transfer struct {
	progress *Progress
	attempt  *attempt
	file     *fileProgress
}

// File starts observing one IPA GET of a package of size bytes.
func (p *Progress) File(share, trace, id string, size int64) *Transfer {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	a := p.open(attemptKey{share, trace}, now)
	if a == nil {
		return &Transfer{}
	}
	f := a.file(id, size)
	if f.active == 0 {
		f.complete = false
	}
	f.requested = true
	f.active++
	f.last = now
	return &Transfer{progress: p, attempt: a, file: f}
}

// Sent records that bytes [start, end) of the file reached the connection.
func (t *Transfer) Sent(start, end int64) {
	if t.progress == nil {
		return
	}
	t.progress.mu.Lock()
	defer t.progress.mu.Unlock()
	f := t.file
	start, end = max(0, start), min(end, f.size)
	if start >= end {
		return
	}
	// Merge overlapping and adjacent ranges, so retries never count twice.
	i := 0
	for i < len(f.spans) && f.spans[i].end < start {
		i++
	}
	j := i
	for j < len(f.spans) && f.spans[j].start <= end {
		start, end = min(start, f.spans[j].start), max(end, f.spans[j].end)
		j++
	}
	if i == j && len(f.spans) >= maxIntervals {
		return
	}
	f.spans = slices.Replace(f.spans, i, j, interval{start, end})
	f.last = time.Now()
	t.attempt.last = f.last
}

// Finish ends the request; a successful one completes the file once all of it was sent.
func (t *Transfer) Finish(success bool) {
	if t.progress == nil {
		return
	}
	t.progress.mu.Lock()
	defer t.progress.mu.Unlock()
	f := t.file
	f.active--
	// A canceled parallel request must not undo a successful complete transfer.
	if success && f.size > 0 && len(f.spans) == 1 && f.spans[0] == (interval{0, f.size}) {
		f.complete = true
	}
	f.last = time.Now()
	t.attempt.last = f.last
}

// Snapshot reports the attempt's stage and the share of all its bytes delivered.
func (p *Progress) Snapshot(share, trace string) wire.InstallationProgress {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	a := p.find(attemptKey{share, trace}, now)
	if a == nil {
		return wire.InstallationProgress{Stage: wire.InstallWaiting}
	}
	requested, transferring, interrupted, unfinished := false, false, false, false
	skipped := 0
	var sent, total int64
	sizesKnown := true
	for _, f := range a.files {
		done := f.complete && f.active == 0
		requested = requested || f.requested
		transferring = transferring || f.active > 0
		if f.requested && !done {
			unfinished = true
			interrupted = interrupted || f.active == 0 && now.Sub(f.last) >= interruptionDelay
		}
		if !f.requested {
			skipped++
		}
		sizesKnown = sizesKnown && f.size > 0
		total += f.size
		for _, span := range f.spans {
			sent += span.end - span.start
		}
	}
	result := wire.InstallationProgress{Stage: wire.InstallWaiting}
	// Every package counts with its full size, including those not yet requested;
	// 100% waits until every response has finished.
	if sizesKnown && total > 0 {
		result.Percent = min(99, int(float64(sent)/float64(total)*100))
	}
	// Everything the device asked for has been delivered: the attempt is complete
	// at once if it asked for every package, or once it has been quiet long enough.
	delivered := a.manifestRequested && !a.manifestFailed && a.manifests == 0 && len(a.files) > 0 && !unfinished
	switch {
	case delivered && (skipped == 0 || now.Sub(a.last) >= settleDelay):
		result.Stage, result.Percent, result.Skipped = wire.InstallComplete, 100, skipped
	case transferring:
		result.Stage = wire.InstallTransferring
	case interrupted:
		result.Stage = wire.InstallInterrupted
	case requested:
		result.Stage = wire.InstallTransferring
	case a.manifests > 0:
		result.Stage = wire.InstallManifest
	case a.manifestFailed:
		result.Stage = wire.InstallFailed
	case a.manifestRequested:
		result.Stage = wire.InstallWaitingIpa
	}
	return result
}
