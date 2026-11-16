//go:build linux && !386 && !s390x

package netcompat

import "syscall"

func acceptSocket(fd int) (int, error) {
	accepted, _, errno := syscall.Syscall(syscall.SYS_ACCEPT, uintptr(fd), 0, 0)
	if errno != 0 {
		return -1, errno
	}
	return int(accepted), nil
}
