package debuglog

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strings"

	"appleseum/backend/internal/rawlog"
	"appleseum/backend/internal/wire"
)

// Log local failures alongside HTTP exchanges, unwrapping single-cause errors.
func (s *Service) SystemError(operation string, err error) {
	if err == nil || errors.Is(err, context.Canceled) {
		return
	}
	rawlog.Log("system operation=%s error=%s", operation, rawlog.Error(err))
	for cause := errors.Unwrap(err); cause != nil; cause = errors.Unwrap(err) {
		err = cause
	}
	s.Event(wire.DebugEvent{Source: "System", Target: operation, Detail: err.Error()})
}

func (s *Service) ExportError(w http.ResponseWriter, r *http.Request, operation string, err error) {
	if response, ok := w.(*debugResponse); ok && response.detail == "response_write" {
		return // The failed HTTP write is already attached to its exchange.
	}
	if r.Context().Err() == nil {
		s.SystemError(operation, err)
	}
}

// A broken journal cannot reliably report its own failure through the journal.
// Report once per process rather than flooding the console on every request.
func (s *Service) logUnavailable(err error) {
	s.log.failure.Do(func() { log.Printf("Journal unavailable: %v", err) })
}

// net/http otherwise prints routine TLS errors and recovered handler panics to
// stderr. Keep these nonfatal failures in the journal, without peer/panic values.
func (s *Service) HttpErrorLog() *log.Logger { return log.New(httpErrorWriter{s}, "", 0) }

type httpErrorWriter struct{ server *Service }

func (w httpErrorWriter) Write(data []byte) (int, error) {
	code := "http_server_error"
	switch {
	case strings.HasPrefix(string(data), "http: panic serving "):
		code = "handler_panic"
	case strings.Contains(string(data), "TLS handshake error"):
		code = "tls_handshake_failed"
	}
	w.server.SystemError("http_server", errors.New(code))
	rawlog.Log("HTTP server error=%s", code)
	return len(data), nil
}
