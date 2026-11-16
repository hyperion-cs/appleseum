package api

import (
	"fmt"
	"net/http"
	"strconv"

	"appleseum/backend/internal/sharing"
)

// deliveryProgress reports the IPA bytes the response writer accepted.
// ServeContent stays responsible for ranges, conditional requests and framing.
// A single range is counted at its offset; a multi-range response, which
// iOS does not request, is delivered but not counted.
type deliveryProgress struct {
	http.ResponseWriter
	transfer                *sharing.Transfer
	status                  int
	offset, written, length int64
	counting, failed        bool
}

func (w *deliveryProgress) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *deliveryProgress) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
		w.length, _ = strconv.ParseInt(w.Header().Get("Content-Length"), 10, 64)
		switch status {
		case http.StatusOK:
			w.counting = true
		case http.StatusPartialContent:
			w.offset, w.counting = rangeStart(w.Header().Get("Content-Range"))
		}
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *deliveryProgress) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(b)
	w.written += int64(n)
	w.failed = w.failed || err != nil
	if w.counting {
		w.transfer.Sent(w.offset, w.offset+int64(n))
		w.offset += int64(n)
	}
	return n, err
}

// finish ends the observation once the handler has returned.
func (w *deliveryProgress) finish(r *http.Request) {
	sent := !w.failed && r.Context().Err() == nil && w.written == w.length &&
		(w.status == http.StatusOK || w.status == http.StatusPartialContent)
	// A conditional request has no payload and is not a broken transfer.
	w.transfer.Finish(sent || w.status == http.StatusNotModified)
}

// rangeStart reads the first byte of a single-range Content-Range header.
func rangeStart(header string) (int64, bool) {
	var start, end, size int64
	n, err := fmt.Sscanf(header, "bytes %d-%d/%d", &start, &end, &size)
	return start, err == nil && n == 3 && start >= 0 && end >= start && end < size
}

func requestTrace(w http.ResponseWriter, r *http.Request) (string, bool) {
	trace := r.URL.Query().Get("trace_id")
	if trace != "" && !sharing.ValidTrace(trace) {
		w.WriteHeader(http.StatusBadRequest)
		return "", false
	}
	return trace, true
}

func tracedPath(path, trace string) string {
	if trace == "" {
		return path
	}
	// Trace IDs are validated ASCII letters and digits, so no escaping is needed.
	return path + "?trace_id=" + trace
}
