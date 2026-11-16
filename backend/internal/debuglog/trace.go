package debuglog

import (
	"bytes"
	"cmp"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"net/http/httputil"
	"slices"
	"strings"
	"time"

	"github.com/hyperion-cs/appleseum/applekit"

	"appleseum/backend/internal/fault"
	"appleseum/backend/internal/wire"
)

type debugResponse struct {
	http.ResponseWriter
	status  int
	body    bytes.Buffer
	size    int64
	capture *applekit.ExchangeCapture
	proto   string
	detail  string
}

func (w *debugResponse) SetErrorDetail(detail string) {
	if w.detail == "" {
		w.detail = detail
	}
}

func (w *debugResponse) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *debugResponse) WriteHeader(status int) {
	if status >= 200 && w.status == 0 {
		w.status = status
		if w.capture != nil {
			w.capture.Write([]byte("\r\n\r\n----- RESPONSE -----\r\n"))
			header, _ := httputil.DumpResponse(&http.Response{StatusCode: status, Proto: w.proto, ProtoMajor: 1, ProtoMinor: 1, Header: w.Header().Clone(), ContentLength: -1}, false)
			w.capture.Write(header)
		}
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *debugResponse) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(p)
	if err != nil && w.detail == "" {
		w.detail = "response_write"
	}
	w.size += int64(n)
	if w.capture != nil {
		w.capture.Write(p[:n])
	} else if left := responsePreviewBytes - w.body.Len(); left > 0 {
		w.body.Write(p[:min(n, left)])
	}
	return n, err
}

// Trace records each request the handler serves, with its captured exchange.
func (s *Service) Trace(source string, handler http.Handler) http.Handler {
	if !s.Enabled() {
		return handler
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if quiet(r.URL.Path) {
			handler.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		conn, _ := r.Context().Value(debugConnKey{}).(*debugConn)
		if conn != nil {
			conn.tlsState(r)
		}
		capture := s.createExchangeCapture()
		readErr := captureRequest(w, r, capture, conn)
		response := &debugResponse{ResponseWriter: w, proto: r.Proto}
		if conn == nil {
			response.capture = capture
		}
		r = r.WithContext(s.Context(r.Context()))
		if readErr != nil {
			response.SetErrorDetail(string(fault.InvalidRequest))
			response.Header().Set("Content-Type", "application/json; charset=utf-8")
			response.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(response).Encode(wire.Error{Code: fault.InvalidRequest})
		} else {
			handler.ServeHTTP(response, r)
		}
		event := wire.DebugEvent{
			Source:     source,
			Method:     method(r.Method),
			Target:     s.target(source, r),
			Status:     cmp.Or(response.status, http.StatusOK),
			DurationMs: time.Since(start).Milliseconds(),
			Detail:     response.detail,
		}
		if capture == nil {
			s.Event(event)
			return
		}
		if response.status == 0 {
			response.WriteHeader(event.Status)
		}
		event.HasResponse, event.ResponseBytes = true, response.size
		if conn == nil {
			capture.Close()
			s.add(event, capture.Filename)
			return
		}
		// The connection writes the final framing after the handler returns.
		conn.mu.Lock()
		conn.finish = func() { s.add(event, capture.Filename) }
		conn.mu.Unlock()
	})
}

// quiet paths are polls, assets and the journal itself, which would flood it.
func quiet(path string) bool {
	return path == "/debug" || path == "/api/state" || path == "/api/health" ||
		(strings.HasPrefix(path, "/api/shares/") && strings.Contains(path, "/progress/")) ||
		strings.HasPrefix(path, "/api/debug") || strings.HasPrefix(path, "/assets/")
}

func method(m string) string {
	if slices.Contains([]string{"GET", "HEAD", "POST", "PUT", "DELETE", "OPTIONS", "PATCH"}, m) {
		return m
	}
	return "OTHER"
}

// target names a request by its route, never by its path values. Hosting
// routes use fixed templates; the interface's top-level files keep their names.
func (s *Service) target(source string, r *http.Request) string {
	target := r.Pattern
	if _, path, found := strings.Cut(target, " "); found {
		target = path
	}
	switch {
	case source == "Hosting" && strings.HasPrefix(r.URL.Path, "/packages/"):
		return "/packages/{share}/{file}.ipa"
	case source == "Hosting" && strings.HasPrefix(r.URL.Path, "/manifests/"):
		return "/manifests/{share}.plist"
	case target == "/" && s.webFile(strings.TrimPrefix(r.URL.Path, "/")):
		return r.URL.Path
	}
	return cmp.Or(target, "/")
}

func (s *Service) webFile(name string) bool {
	if s.cfg.Web == nil || name == "" || strings.Contains(name, "/") {
		return false
	}
	info, err := fs.Stat(s.cfg.Web, name)
	return err == nil && !info.IsDir()
}

// captureRequest writes the request head to the capture and spools its body
// there before the handler validates it, so early rejections keep the whole
// body. The handler reads the spooled copy back without buffering uploads in memory.
func captureRequest(w http.ResponseWriter, r *http.Request, capture *applekit.ExchangeCapture, conn *debugConn) error {
	if capture == nil {
		return nil
	}
	if conn != nil {
		conn.begin(capture)
		defer conn.responseStart()
	}
	captured := r.Clone(r.Context())
	captured.Header.Del("X-Session-Key")
	captured.URL.Host, captured.URL.Scheme = r.Host, "http"
	if r.TLS != nil {
		captured.URL.Scheme = "https"
	}
	captured.RequestURI = captured.URL.String()
	header, _ := httputil.DumpRequest(captured, false)
	capture.Write(header)
	file, ok := capture.WriteCloser.(interface {
		io.Writer
		io.ReaderAt
		io.Seeker
	})
	if r.Body == nil || r.Body == http.NoBody || !ok {
		return nil
	}
	controller := http.NewResponseController(w)
	controller.SetReadDeadline(time.Now().Add(captureReadTimeout))
	defer controller.SetReadDeadline(time.Time{})
	offset, err := file.Seek(0, io.SeekCurrent)
	if err != nil {
		return err
	}
	n, err := io.Copy(file, r.Body)
	r.Body.Close()
	if err != nil {
		return err
	}
	r.Body = io.NopCloser(io.NewSectionReader(file, offset, n))
	return nil
}
