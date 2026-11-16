package rawlog

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

const (
	maxBytes = 8_388_608
	backups  = 2
)

var current atomic.Pointer[recorder]
var lifecycle sync.Mutex

type operation struct {
	name  string
	start time.Time
}

type recorder struct {
	ctx         context.Context
	cancel      context.CancelFunc
	started     time.Time
	sequence    atomic.Uint64
	pending     sync.Map
	connections sync.Map
	accepted    atomic.Uint64
	closed      atomic.Uint64
	output      *rotatingFile
	warned      atomic.Bool
}

type rotatingFile struct {
	mu       sync.Mutex
	path     string
	file     *os.File
	identity os.FileInfo
	closed   bool
	limit    int64
	redirect func(*os.File) error
	restore  func() error
}

// Start enables raw logging and captures stderr. The returned function stops both.
func Start(path string) (func() error, error) {
	lifecycle.Lock()
	defer lifecycle.Unlock()
	if current.Load() != nil {
		return nil, errors.New("raw logging is already active")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	f, err := openLog(abs)
	if err != nil {
		return nil, err
	}
	redirect, restore, err := captureStderr(f)
	if err != nil {
		f.Close()
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	r := &recorder{
		ctx:     ctx,
		cancel:  cancel,
		started: time.Now(),
		output:  &rotatingFile{path: abs, file: f, limit: maxBytes, redirect: redirect, restore: restore},
	}
	current.Store(r)
	r.start()
	return r.close, nil
}

// Close finishes the active log after an ordinary application return. Keeping
// it open while a panic unwinds lets the runtime write the crash report there.
func Close() error {
	if r := current.Load(); r != nil {
		return r.close()
	}
	return nil
}

func (r *recorder) close() error {
	lifecycle.Lock()
	defer lifecycle.Unlock()
	if !current.CompareAndSwap(r, nil) {
		return nil
	}
	r.cancel()
	r.output.mu.Lock()
	defer r.output.mu.Unlock()
	r.output.closed = true
	err := r.output.restore()
	var closeErr error
	if r.output.file != nil {
		closeErr = r.output.file.Close()
		r.output.file = nil
	}
	return errors.Join(err, closeErr)
}

func (r *recorder) log(format string, args ...any) {
	line := fmt.Sprintf("RAW %s +%.3fs %s\n", time.Now().Format(time.RFC3339Nano), time.Since(r.started).Seconds(), fmt.Sprintf(format, args...))
	if _, err := r.output.Write([]byte(line)); err != nil && r.warned.CompareAndSwap(false, true) {
		fmt.Fprintf(os.Stdout, "Could not write raw debug log: %v\n", err)
	}
}

func (w *rotatingFile) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return len(p), nil
	}
	if w.identity == nil && w.file != nil {
		info, err := w.file.Stat()
		if err != nil {
			return 0, err
		}
		w.identity = info
	}
	// Check the pathname, not just the open descriptor: users may remove or
	// replace the log while the application keeps running.
	info, err := statLog(w.path)
	if err != nil || w.file == nil || !os.SameFile(w.identity, info) {
		info, err = w.reopen()
		if err != nil {
			return 0, err
		}
	}
	if info.Size()+int64(len(p)) > w.limit {
		if err = w.rotate(); err != nil {
			return 0, err
		}
	}
	return w.file.Write(p)
}

func (w *rotatingFile) reopen() (os.FileInfo, error) {
	// Windows may keep a deleted pathname pending until its last handle is
	// closed. Release our old file before recreating it, and allow a later
	// write to retry if opening the replacement fails.
	if w.file != nil {
		err := w.file.Close()
		w.file, w.identity = nil, nil
		if err != nil {
			return nil, err
		}
	}
	f, err := openLog(w.path)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err == nil && w.redirect != nil {
		err = w.redirect(f)
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	w.file, w.identity = f, info
	return info, nil
}

func (w *rotatingFile) rotate() error {
	for i := backups; i >= 1; i-- {
		destination := fmt.Sprintf("%s.%d", w.path, i)
		if err := os.Remove(destination); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		source := w.path
		if i > 1 {
			source = fmt.Sprintf("%s.%d", w.path, i-1)
		}
		if err := os.Rename(source, destination); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	_, err := w.reopen()
	return err
}
