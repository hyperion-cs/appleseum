//go:build linux && 386

package main

import (
	"fmt"
	"os"
	"syscall"
)

func prepareIshRuntime(ish bool) error {
	if !ish {
		return nil
	}
	environment, changed := runtimeEnvironmentForIsh(os.Environ())
	if !changed {
		return nil
	}
	path, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate executable for iSH compatibility: %w", err)
	}
	// These runtime options are read before main. Replace the process once,
	// preserving its PID, arguments, working directory and standard streams.
	return syscall.Exec(path, os.Args, environment)
}
