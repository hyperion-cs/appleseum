package debuglog

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"appleseum/backend/internal/fault"
	"appleseum/backend/internal/product"
	"appleseum/backend/internal/rawlog"
	"appleseum/backend/internal/wire"
)

// contextWriter stops a long export when its request ends.
type contextWriter struct {
	ctx context.Context
	w   io.Writer
}

func (w contextWriter) Write(data []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	return w.w.Write(data)
}

// Events returns the journal's events, newest first.
func (s *Service) Events() []wire.DebugEvent {
	s.log.mu.Lock()
	defer s.log.mu.Unlock()
	entries := s.log.entries()
	events := make([]wire.DebugEvent, len(entries))
	for i, e := range entries {
		events[i] = e.event
	}
	return events
}

// Process describes the running server for the journal's header.
func (s *Service) Process() wire.DebugInfo {
	j := &s.log
	return wire.DebugInfo{
		Executable:    j.executable,
		Arguments:     j.arguments,
		StartedAt:     j.started.UTC().Format(time.RFC3339),
		UptimeSeconds: int64(time.Since(j.started).Seconds()),
		Version:       product.Version,
		Runtime:       runtime.Version() + " " + runtime.GOOS + "/" + runtime.GOARCH,
		Goroutines:    runtime.NumGoroutine(),
		HeapBytes:     rawlog.HeapBytes(),
		Capacity:      capacity,
	}
}

// Selected lists the matching events that have a captured exchange.
func (s *Service) Selected(filter Filter) ([]int64, error) {
	ids := []int64{}
	for _, event := range s.Events() {
		if event.HasResponse && filter.Match(event) {
			if ids = append(ids, event.Id); len(ids) > wire.MaxSelectionSize {
				return nil, fault.SelectionTooLarge
			}
		}
	}
	return ids, nil
}

// Clear deletes every event and captured exchange and starts a new segment.
func (s *Service) Clear() error {
	j := &s.log
	j.mu.Lock()
	defer j.mu.Unlock()
	j.closeCsv()
	for len(j.segments) > 0 {
		j.drop()
	}
	err := j.rotate()
	if err != nil {
		s.logUnavailable(err)
	}
	return err
}

// Exchanges are captured exchanges opened for export.
type Exchanges struct {
	files    []debugHarFile
	Modified time.Time
}

// OpenExchanges opens the captures of the given events; all of them must exist.
func (s *Service) OpenExchanges(ids []int64) (*Exchanges, error) {
	j := &s.log
	j.mu.Lock()
	defer j.mu.Unlock()
	opened := &Exchanges{}
	for _, id := range ids {
		e, found := j.find(id)
		if !found || e.file == "" {
			opened.Close()
			return nil, fault.NotFound
		}
		file, err := os.Open(filepath.Join(j.directory, e.file))
		if err == nil {
			opened.files = append(opened.files, debugHarFile{file: file, event: e.event})
			var info os.FileInfo
			if info, err = file.Stat(); err == nil && info.ModTime().After(opened.Modified) {
				opened.Modified = info.ModTime()
			}
		}
		if err != nil {
			s.logUnavailable(err)
			opened.Close()
			return nil, fault.NotFound
		}
	}
	return opened, nil
}

func (e *Exchanges) Close() {
	for _, file := range e.files {
		file.file.Close()
	}
}

// Raw is the first capture as it was written to disk.
func (e *Exchanges) Raw() *os.File { return e.files[0].file }

func (e *Exchanges) WriteHar(ctx context.Context, w io.Writer) error {
	return writeDebugHar(ctx, w, e.files)
}

// Csv renders the journal as it is now, oldest first, and returns the time of its newest event.
func (s *Service) Csv() ([]byte, time.Time) {
	s.log.mu.Lock()
	defer s.log.mu.Unlock()
	var out bytes.Buffer
	s.log.writeCsv(&out)
	var modified time.Time
	if entries := s.log.entries(); len(entries) > 0 {
		modified, _ = time.Parse(time.RFC3339Nano, entries[0].event.At)
	}
	return out.Bytes(), modified
}
