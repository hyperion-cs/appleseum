//go:build !linux && !windows

package rawlog

import "golang.org/x/sys/unix"

func duplicateStderr(fd int) error { return unix.Dup2(fd, 2) }
