package debuglog

import (
	"context"
	"io"
	"os"
	"path/filepath"

	"github.com/hyperion-cs/appleseum/applekit"

	"appleseum/backend/internal/rawlog"
	"appleseum/backend/internal/wire"
)

// Context makes Apple requests made under ctx report to the journal and capture their exchanges.
func (s *Service) Context(ctx context.Context) context.Context {
	ctx = applekit.WithRequestObserver(ctx, s.ObserveApple)
	return applekit.WithExchangeCapture(ctx, s.createExchangeCapture)
}

func (s *Service) ObserveApple(e applekit.RequestEvent) {
	rawlog.Log("apple response body finished method=%s host=%s status=%d headers_ms=%d error_class=%s", e.Method, e.Host, e.Status, e.DurationMs, e.Error)
	event := wire.DebugEvent{
		Source:     "Apple",
		Method:     e.Method,
		Target:     e.Host + " " + e.Path,
		Status:     e.Status,
		DurationMs: e.DurationMs,
		Detail:     e.Error,
	}
	switch {
	case e.ExchangeFile != "":
		event.HasResponse, event.ResponseBytes = true, e.ResponseBytes
		s.add(event, e.ExchangeFile)
	case e.Status != 0 && s.Enabled():
		// Without a capture applekit keeps a bounded copy of the response body.
		name := newName("exchange-", ".http")
		if err := os.WriteFile(filepath.Join(s.log.directory, name), e.ResponseBody, 0o600); err != nil {
			s.logUnavailable(err)
			name = ""
		} else {
			event.HasResponse, event.ResponseBytes, event.ResponseTruncated = true, e.ResponseBytes, e.Truncated
		}
		s.add(event, name)
	default:
		s.Event(event)
	}
}

func (s *Service) createExchangeCapture() *applekit.ExchangeCapture {
	name := newName("exchange-", ".http")
	f, err := os.OpenFile(filepath.Join(s.log.directory, name), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		s.logUnavailable(err)
		return nil
	}
	return &applekit.ExchangeCapture{WriteCloser: &captureFile{File: f, server: s}, Filename: name}
}

// captureFile reports its write failures as journal failures.
type captureFile struct {
	*os.File
	server *Service
}

func (f *captureFile) Write(data []byte) (int, error) {
	n, err := f.File.Write(data)
	if err != nil {
		f.server.logUnavailable(err)
	}
	return n, err
}

func (f *captureFile) Close() error {
	err := f.File.Close()
	if err != nil {
		f.server.logUnavailable(err)
	}
	return err
}

func (f *captureFile) ReadFrom(r io.Reader) (int64, error) {
	// Force writes through Write: a failed request-body read is not a journal
	// failure, even though os.File.ReadFrom wraps both kinds of error alike.
	return io.Copy(struct{ io.Writer }{f}, r)
}
