// Package env identifies the host environment shared by applekit and its callers.
package env

import (
	"os"
	"runtime"
	"sync"
)

var detectIsh = sync.OnceValue(func() bool {
	if runtime.GOOS != "linux" || runtime.GOARCH != "386" {
		return false
	}
	info, err := os.Stat("/proc/ish/version")
	return err == nil && !info.IsDir()
})

// IsIsh detects iSH once per process.
func IsIsh() bool { return detectIsh() }
