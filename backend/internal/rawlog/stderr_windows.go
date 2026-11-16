package rawlog

import (
	"log"
	"os"

	"golang.org/x/sys/windows"
)

func statLog(path string) (os.FileInfo, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	// os.Stat defers file identity lookup to a handle without write sharing,
	// which conflicts with our open log. Stat a shared handle instead.
	handle, err := windows.CreateFile(name, windows.FILE_READ_ATTRIBUTES, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(handle), path)
	defer f.Close()
	return f.Stat()
}

func openLog(path string) (*os.File, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	// Allow rotation while the golang runtime still owns a stderr handle.
	handle, err := windows.CreateFile(name, windows.FILE_APPEND_DATA, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_ALWAYS, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(handle), path), nil
}

func captureStderr(file *os.File) (func(*os.File) error, func() error, error) {
	saved, err := windows.GetStdHandle(windows.STD_ERROR_HANDLE)
	if err != nil {
		return nil, nil, err
	}
	writer := log.Writer()

	set := func(f *os.File) error {
		if err := windows.SetStdHandle(windows.STD_ERROR_HANDLE, windows.Handle(f.Fd())); err != nil {
			return err
		}
		log.SetOutput(f)
		return nil
	}

	if err = set(file); err != nil {
		return nil, nil, err
	}

	restore := func() error { log.SetOutput(writer); return windows.SetStdHandle(windows.STD_ERROR_HANDLE, saved) }

	return set, restore, nil
}
