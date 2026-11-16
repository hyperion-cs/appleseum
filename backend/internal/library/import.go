package library

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"os"
	"path/filepath"
	"time"

	"github.com/hyperion-cs/appleseum/applekit"
	"github.com/hyperion-cs/appleseum/applekit/ipa"

	"appleseum/backend/internal/db"
	"appleseum/backend/internal/fault"
	"appleseum/backend/internal/wire"
)

const (
	uploadProgressInterval = 250 * time.Millisecond
)

// ImportedAppId gives an imported app a negative id, apart from App Store ids.
func ImportedAppId(bundle string) int64 {
	sum := sha256.Sum256([]byte(bundle))
	return -1 - int64(binary.BigEndian.Uint64(sum[:8])>>1)
}

func inspectImport(path string) (ipa.Metadata, error) {
	m, err := ipa.Inspect(path)
	if err != nil || !wire.Text(m.Bundle, wire.MaxBundleIdLength) || !wire.Text(m.Version, wire.MaxMetadataLength) ||
		!wire.Text(m.DisplayVersion, wire.MaxMetadataLength) || !wire.Text(m.MinimumOs, wire.MaxMetadataLength) || !wire.Text(m.Name, wire.MaxFilenameBytes) {
		return ipa.Metadata{}, fault.InvalidIpa
	}
	if m.Name == "" {
		m.Name = m.Bundle
	}
	return m, nil
}

// Upload receives one IPA as a running transfer that Cancel can stop.
type Upload struct {
	s         *Service
	id, owner string
	total     int64
	file      *os.File
	received  int64
	reported  time.Time
	progress  func(applekit.DownloadProgress)
	untrack   func()
	published bool
}

// UploadSlot takes one of the few upload slots without waiting; release it when the upload ends.
func (s *Service) UploadSlot(owner string) (release func(), err error) {
	return s.slots.acquire(context.Background(), owner, s.public, true, false)
}

// BeginUpload records an upload as a running transfer.
func (s *Service) BeginUpload(ctx context.Context, cancel context.CancelFunc, id, owner, sessionId, name string, total int64) (*Upload, error) {
	u := &Upload{s: s, id: id, owner: owner, total: total, progress: s.Progress(id)}
	if err := u.begin(ctx, cancel, sessionId, name); err != nil {
		u.cleanup()
		return nil, err
	}
	return u, nil
}

func (u *Upload) begin(ctx context.Context, cancel context.CancelFunc, sessionId, name string) error {
	if u.s.Running(u.id) {
		return fault.Busy
	}
	u.untrack = u.s.Track(u.id, cancel)
	if taken, err := u.s.db.PackageExists(ctx, u.id); err != nil || taken {
		// An id names one upload; reusing it must not replace a saved package.
		return cmp.Or(err, error(fault.Busy))
	}
	file, err := os.CreateTemp(filepath.Join(u.s.db.Directory(), "packages"), ".import-*.tmp")
	if err != nil {
		return err
	}
	u.file = file
	return u.s.db.BeginImport(ctx, db.Import{Id: u.id, Owner: u.owner, SessionId: sessionId, Name: name, AppId: ImportedAppId("pending:" + u.id), Total: u.total})
}

func (u *Upload) cleanup() {
	if u.file != nil {
		u.file.Close()
		u.s.RemoveTemporary(u.file.Name())
	}
	if u.untrack != nil {
		u.untrack()
	}
}

// Write stores received bytes and reports progress a few times per second.
func (u *Upload) Write(p []byte) (int, error) {
	if time.Since(u.reported) >= uploadProgressInterval {
		if err := u.s.CheckStorage(); err != nil {
			return 0, err
		}
	}
	if int64(len(p)) > MaxTransferBytes-u.received {
		return 0, fault.FileTooLarge
	}
	n, err := u.file.Write(p)
	if err != nil {
		u.s.StorageFailure(err)
	}
	u.received += int64(n)
	if time.Since(u.reported) >= uploadProgressInterval {
		u.progress(applekit.DownloadProgress{Received: u.received, Total: u.total, Stage: "downloading"})
		u.reported = time.Now()
	}
	return n, err
}

// Publish checks the received IPA and moves it into the owner's library.
func (u *Upload) Publish(ctx context.Context) error {
	u.progress(applekit.DownloadProgress{Received: u.received, Total: u.received, Stage: "downloading"})
	u.progress(applekit.DownloadProgress{Stage: "preparing"})
	if err := u.file.Close(); err != nil {
		return err
	}
	metadata, err := inspectImport(u.file.Name())
	if err != nil {
		return err
	}
	// The uploader owns the file, whichever account its signature names.
	appId := ImportedAppId(metadata.Bundle)
	if err = u.s.db.PublishImport(ctx, u.id, u.owner, appId, u.file.Name(), metadata, u.received); err != nil {
		return err
	}
	u.published = true
	u.s.Icons.FromIpa(ctx, u.owner, appId, u.s.db.PackagePath(u.id))
	return nil
}

// Close ends the transfer; an upload that was not published records why.
func (u *Upload) Close(ctx context.Context, failure error) {
	defer u.cleanup()
	if u.published {
		return
	}
	status, code := wire.Failed, fault.Of(failure)
	if ctx.Err() != nil {
		status, code = wire.Canceled, ""
	}
	u.s.report("ipa_import", u.s.db.FinishDownload(context.WithoutCancel(ctx), u.id, status, code))
}
