package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestIshBackgroundHoldsDeviceWithoutReadingAndReleasesIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "location")
	if err := os.WriteFile(path, []byte("location data must not be read"), 0600); err != nil {
		t.Fatal(err)
	}

	findFd := func() string {
		entries, err := os.ReadDir("/proc/self/fd")
		if err != nil {
			t.Skip(err)
		}
		for _, entry := range entries {
			if target, err := os.Readlink("/proc/self/fd/" + entry.Name()); err == nil && target == path {
				return entry.Name()
			}
		}
		return ""
	}

	release := startIshBackground(path)
	defer release()
	runtime.GC()
	fd := findFd()
	if fd == "" {
		t.Fatal("location descriptor was not retained")
	}
	info, err := os.ReadFile("/proc/self/fdinfo/" + fd)
	if err != nil {
		t.Fatal(err)
	}
	var position int64
	if _, err := fmt.Sscanf(string(info), "pos:\t%d", &position); err != nil || position != 0 {
		t.Fatalf("location file was read: position %d, %v", position, err)
	}
	release()
	release()
	if findFd() != "" {
		t.Fatal("location descriptor was not released")
	}
}
