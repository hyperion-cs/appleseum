//go:build linux && (386 || s390x)

package netcompat

import (
	"syscall"
	"unsafe"
)

func acceptSocket(fd int) (int, error) {
	// syscall.Accept and unix.Accept both call accept4 on Linux. Operation 5
	// is the original accept; passing null addresses leaves lookup to FileConn.
	args := [...]uintptr{uintptr(fd), 0, 0}
	accepted, _, errno := syscall.Syscall(syscall.SYS_SOCKETCALL, 5, uintptr(unsafe.Pointer(&args[0])), 0)
	if errno != 0 {
		return -1, errno
	}
	return int(accepted), nil
}
