package api

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/synctest"
	"time"

	"appleseum/backend/internal/sharing"
	"appleseum/backend/internal/wire"
)

type interruptedResponse struct {
	*httptest.ResponseRecorder
	remaining int
}

func (w *interruptedResponse) Write(b []byte) (int, error) {
	n := min(w.remaining, len(b))
	w.remaining -= n
	w.ResponseRecorder.Write(b[:n])
	if n < len(b) {
		return n, errors.New("connection lost")
	}
	return n, nil
}

func TestDeliveryProgressRanges(t *testing.T) {
	for _, test := range []struct {
		name, ranges string
		percent      int
		fail         bool
	}{
		{"full", "", 100, false},
		{"tail", "bytes=80-99", 20, false},
		{"suffix", "bytes=-20", 20, false},
		{"multipart", "bytes=0-9,80-99", 0, false},
		{"multipart overlap", "bytes=0-19,10-29", 0, false},
		{"invalid range", "bytes=1000-", 0, false},
		{"aborted full", "", 43, true},
		{"aborted multipart", "bytes=0-19,80-99", 0, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				p := new(sharing.Progress)
				p.Manifest("share", "trace01", []sharing.Package{{Id: "file", Size: 100}})(true)
				req := httptest.NewRequest("GET", "/file.ipa", nil)
				if test.ranges != "" {
					req.Header.Set("Range", test.ranges)
				}
				response := httptest.NewRecorder()
				var destination http.ResponseWriter = response
				if test.fail {
					destination = &interruptedResponse{response, 43}
				}
				response.Header().Set("Content-Type", "application/octet-stream")
				observed := &deliveryProgress{ResponseWriter: destination, transfer: p.File("share", "trace01", "file", 100)}
				http.ServeContent(observed, req, "file.ipa", time.Time{}, bytes.NewReader(make([]byte, 100)))
				observed.finish(req)
				time.Sleep(3 * time.Second)
				synctest.Wait()
				got := p.Snapshot("share", "trace01")
				want := wire.InstallInterrupted
				if test.percent == 100 {
					want = wire.InstallComplete
				}
				if got.Percent != test.percent || got.Stage != want {
					t.Fatalf("progress: %+v", got)
				}
			})
		})
	}
}
