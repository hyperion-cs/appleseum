package session

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/hyperion-cs/appleseum/applekit"
	"github.com/hyperion-cs/appleseum/applekit/ipa"

	"appleseum/backend/internal/account"
	"appleseum/backend/internal/fault"
	"appleseum/backend/internal/library"
	"appleseum/backend/internal/wire"
)

const (
	downloadTimeout = 30 * time.Minute
)

// wakeWorker starts the queue; requires mu. The worker keeps the session key
// leased until the queue is empty.
func (w *Workspace) wakeWorker() {
	if !w.workerLease {
		if !w.retainSecrets() {
			return
		}
		w.workerLease = true
	}
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

// Requires mu.
func (w *Workspace) releaseWorkerLease() {
	if w.workerLease {
		w.workerLease = false
		w.releaseSecretsLocked()
	}
}

// work drains the queue with three workers. Shared slots also limit other
// sessions of the same account and all transfers on the server.
func (w *Workspace) work() {
	done := make(chan bool, library.AccountTransfers)
	active := 0
	defer func() { w.mu.Lock(); w.releaseWorkerLease(); w.mu.Unlock() }()
	for {
		launch := false
		select {
		case <-w.ctx.Done():
			for active > 0 {
				<-done
				active--
			}
			return
		case <-w.wake:
			launch = true
		case more := <-done:
			active--
			launch = more
		}
		if launch && w.ctx.Err() == nil {
			for active < library.AccountTransfers {
				active++
				go func() { done <- w.download() }()
			}
		}
		w.mu.Lock()
		if active == 0 && len(w.wake) == 0 {
			w.releaseWorkerLease()
		}
		w.mu.Unlock()
	}
}

// download runs the next queued job and reports whether the queue may hold more.
func (w *Workspace) download() bool {
	s, lib := w.r.db, w.r.library
	if queued, err := s.Queued(w.ctx, w.Id); err != nil || !queued {
		lib.StorageFailure(err)
		w.r.cfg.Report("download_queue", err)
		return false
	}
	w.mu.Lock()
	identity, epoch, waiting := w.identity, w.accountCtx, w.login.required
	w.mu.Unlock()
	if identity == nil || waiting || epoch.Err() != nil {
		return false
	}
	if err := lib.WaitStorage(epoch); err != nil {
		return false
	}
	release, err := lib.DownloadSlot(epoch, identity.Owner)
	if err != nil {
		return false
	}
	defer release()
	if err = w.apple.Lock(epoch); err != nil {
		return false
	}
	w.mu.Lock()
	current := w.accountCtx == epoch && epoch.Err() == nil && !w.login.required
	w.mu.Unlock()
	if !current {
		w.apple.Unlock()
		return false
	}
	scope := w.Actor().Scope()
	scope.Owner = identity.Owner
	job, claimed, err := s.ClaimDownload(epoch, scope, w.Id)
	if err != nil || !claimed {
		w.apple.Unlock()
		lib.StorageFailure(err)
		w.r.cfg.Report("download_queue", err)
		return false
	}
	ctx, cancel := context.WithTimeout(epoch, downloadTimeout)
	defer cancel()
	defer lib.Track(job.Id, cancel)()
	partial := filepath.Join(s.Directory(), "packages", job.Id+".partial")
	defer lib.RemoveTemporary(partial)
	defer lib.RemoveTemporary(partial + ".tmp")
	finish := func(status wire.DownloadStatus, code fault.Code) bool {
		err := s.FinishDownload(context.WithoutCancel(ctx), job.Id, status, code)
		lib.StorageFailure(err)
		w.r.cfg.Report("download_complete", err)
		return err == nil
	}
	if canceling, e := s.Canceling(ctx, job.Id); e != nil || canceling {
		w.apple.Unlock()
		if e != nil {
			lib.StorageFailure(e)
			return finish(wire.Failed, fault.Of(e))
		}
		return finish(wire.Canceled, "")
	}
	var transfer applekit.Download
	err = w.call(ctx, func(session account.Session) error {
		var e error
		transfer, e = session.PrepareDownload(ctx, job.AppId, job.VersionId)
		return e
	})
	var code fault.Code
	if err != nil {
		// Session expiry/renewal is handled only during licensing, under apple.
		code = downloadFailure(err)
		if code == fault.DownloadFailed {
			code = w.appleFailure(ctx, err)
			if code != fault.SessionExpired {
				code = fault.DownloadFailed
			}
		}
	}
	w.apple.Unlock()
	capacityWait := false
	if err == nil {
		var unreserve func()
		defer func() {
			if unreserve != nil {
				unreserve()
			}
		}()
		_, err = transfer(ctx, partial, lib.Progress(job.Id), applekit.DownloadOptions{
			MaxBytes:    library.MaxTransferBytes,
			IdleTimeout: 2 * time.Minute,
			BeforeWrite: func(total int64) error {
				var e error
				unreserve, e = lib.ReserveDownload(total)
				capacityWait = fault.Is(e, fault.StorageFull)
				return e
			},
			CheckSpace: lib.CheckStorage,
		})
		if err != nil {
			code = downloadFailure(err)
		}
	}
	if err != nil {
		lib.StorageFailure(err)
		w.r.cfg.Report("ipa_download", err)
	}
	var metadata ipa.Metadata
	var size int64
	if err == nil {
		if metadata, err = ipa.Inspect(partial); err != nil {
			w.r.cfg.Report("ipa_inspect", err)
			code = fault.InvalidPackage
			if fault.Of(err) == fault.StorageFull {
				code = fault.StorageFull
			}
		} else if stat, e := os.Stat(partial); e != nil {
			err = e
			code = fault.Of(e)
		} else {
			size = stat.Size()
		}
	}
	w.mu.Lock()
	waiting = w.accountCtx == epoch && w.login.required
	w.mu.Unlock()
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return finish(wire.Failed, fault.DownloadTimeout)
	case ctx.Err() != nil:
		return finish(wire.Canceled, "")
	case capacityWait || waiting && code != "":
		err := s.RetryDownload(context.WithoutCancel(ctx), job.Id)
		lib.StorageFailure(err)
		w.r.cfg.Report("download_complete", err)
		return err == nil
	case code != "":
		return finish(wire.Failed, code)
	}
	lib.Icons.FromIpa(ctx, identity.Owner, job.AppId, partial)
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return finish(wire.Failed, fault.DownloadTimeout)
	}
	if ctx.Err() != nil {
		return finish(wire.Canceled, "")
	}
	switch err = s.PublishDownload(context.WithoutCancel(ctx), job.Id, partial, metadata, size); {
	case err == nil:
		return true
	case fault.Is(err, fault.Canceled):
		return finish(wire.Canceled, "")
	}
	lib.StorageFailure(err)
	w.r.cfg.Report("download_publish", err)
	return finish(wire.Failed, fault.Of(err))
}

func downloadFailure(err error) fault.Code {
	if errors.Is(err, applekit.ErrDownloadTooLarge) {
		return fault.FileTooLarge
	}
	var path *os.PathError
	var link *os.LinkError
	var code fault.Code
	if fault.Of(err) == fault.StorageFull || errors.As(err, &path) || errors.As(err, &link) {
		return fault.Of(err)
	}
	if errors.As(err, &code) {
		return code
	}
	return fault.DownloadFailed
}
