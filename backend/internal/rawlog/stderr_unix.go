//go:build !windows

package rawlog

import (
	"os"

	"golang.org/x/sys/unix"
)

func statLog(path string) (os.FileInfo, error) { return os.Stat(path) }

func openLog(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return nil, err
	}
	if err = f.Chmod(0600); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

func captureStderr(file *os.File) (func(*os.File) error, func() error, error) {
	saved, err := unix.FcntlInt(2, unix.F_DUPFD_CLOEXEC, 0)
	if err != nil {
		return nil, nil, err
	}

	set := func(f *os.File) error { return duplicateStderr(int(f.Fd())) }

	if err = set(file); err != nil {
		unix.Close(saved)
		return nil, nil, err
	}

	restore := func() error {
		err := duplicateStderr(saved)
		closeErr := unix.Close(saved)
		if err != nil {
			return err
		}
		return closeErr
	}

	return set, restore, nil
}
