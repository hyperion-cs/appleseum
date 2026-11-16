package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"appleseum/backend/internal/fault"
	"appleseum/backend/internal/wire"
)

const (
	eventStream = "application/x-ndjson"
)

// versions answers with one page, or, for a client that accepts a stream,
// with progress events followed by the page.
func (a *Server) versions(w http.ResponseWriter, r *http.Request, v visit) error {
	appId, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || appId <= 0 {
		return fault.InvalidRequest
	}
	input, err := decode[wire.VersionPageRequest](w, r)
	if err == nil {
		err = input.Validate()
	}
	if err != nil {
		return err
	}
	stream := &versionStream{w: w, enabled: r.Header.Get("Accept") == eventStream}
	page, err := v.space.Versions(r.Context(), appId, input, stream.progress)
	switch {
	case r.Context().Err() != nil:
	case err != nil && stream.started:
		// The status line is sent; the failure travels as the last event.
		stream.send(wire.VersionLoadEvent{Progress: stream.last, Error: fault.Of(err)})
	case err != nil:
		return err
	case stream.enabled:
		stream.send(wire.VersionLoadEvent{Progress: 100, Page: &page})
	default:
		writeJson(w, http.StatusOK, page)
	}
	return nil
}

type versionStream struct {
	w       http.ResponseWriter
	enabled bool
	started bool
	failed  bool
	last    int
}

func (s *versionStream) send(event wire.VersionLoadEvent) {
	if s.failed {
		return
	}
	if !s.started {
		s.started = true
		header := s.w.Header()
		header.Set("Content-Type", eventStream+"; charset=utf-8")
		header.Set("Cache-Control", "no-store")
		// Tell a buffering proxy to pass events on as they come.
		header.Set("X-Accel-Buffering", "no")
	}
	if json.NewEncoder(s.w).Encode(event) != nil || http.NewResponseController(s.w).Flush() != nil {
		s.failed = true
	}
}

// progress reports a percentage that only grows; 100 is reserved for the page.
func (s *versionStream) progress(value int) {
	value = min(99, max(0, value))
	if !s.enabled || s.started && value <= s.last {
		return
	}
	s.last = value
	s.send(wire.VersionLoadEvent{Progress: value})
}
