package api

import (
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"appleseum/backend/internal/fault"
	"appleseum/backend/internal/wire"
)

const (
	maxImportBytes     int64 = 4_294_967_296
	importTimeout            = 30 * time.Minute
	importRetrySeconds       = "5"
)

// importIpa receives one IPA as the request body and adds it to the
// uploader's library. The id is chosen by the browser, so it can cancel the
// upload while it runs; the file name comes in Content-Disposition.
func (a *Server) importIpa(w http.ResponseWriter, r *http.Request, v visit) error {
	id := r.PathValue("id")
	if !wire.Uuid(id) {
		return fault.InvalidRequest
	}
	_, params, err := mime.ParseMediaType(r.Header.Get("Content-Disposition"))
	// Only the base name is kept: a client path never reaches the library.
	name := filepath.Base(params["filename"])
	if err != nil || !strings.EqualFold(filepath.Ext(name), ".ipa") || !wire.Text(name, wire.MaxFilenameBytes) {
		return fault.InvalidIpa
	}
	switch {
	case r.ContentLength < 0:
		return refusal{http.StatusLengthRequired, fault.InvalidRequest}
	case r.ContentLength > maxImportBytes:
		return fault.ImportTooLarge
	}
	release, err := a.library.UploadSlot(v.actor.Owner)
	if err != nil {
		w.Header().Set("Retry-After", importRetrySeconds)
		return err
	}
	defer release()
	unreserve, err := a.library.ReserveUpload(r.ContentLength)
	if err != nil {
		return err
	}
	defer unreserve()
	ctx, cancel := v.space.UploadContext(r.Context())
	defer cancel()
	// A large upload may take long, but not forever; a canceled one stops reading at once.
	controller := http.NewResponseController(w)
	controller.SetWriteDeadline(time.Time{})
	controller.SetReadDeadline(time.Now().Add(importTimeout))
	defer controller.SetReadDeadline(time.Time{})
	interrupted := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		controller.SetReadDeadline(time.Now())
		close(interrupted)
	})
	defer func() {
		if !stop() {
			<-interrupted
		}
	}()
	upload, err := a.library.BeginUpload(ctx, cancel, id, v.actor.Owner, v.space.Id, name, r.ContentLength)
	if err != nil {
		return err
	}
	size, err := io.Copy(upload, r.Body)
	switch {
	case ctx.Err() != nil:
		err = fault.Canceled
	case err != nil:
		var path *os.PathError
		if !errors.As(err, &path) && fault.Of(err) != fault.StorageFull && !fault.Is(err, fault.StorageFailed, fault.FileTooLarge) {
			err = fault.ImportFailed
		}
	case size != r.ContentLength:
		err = fault.InvalidRequest
	default:
		err = upload.Publish(ctx)
	}
	upload.Close(ctx, err)
	if err != nil {
		return err
	}
	writeJson(w, http.StatusCreated, wire.Created{Id: id})
	return nil
}
