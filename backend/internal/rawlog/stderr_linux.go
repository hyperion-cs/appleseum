package rawlog

import "golang.org/x/sys/unix"

func duplicateStderr(fd int) error { return unix.Dup3(fd, 2, 0) }
