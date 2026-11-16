package netcompat

import (
	"errors"
	"net"
	"os"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"appleseum/backend/internal/rawlog"
)

type tcpListener struct {
	tcp    *net.TCPListener
	accept func() (net.Conn, error)

	acceptMu sync.Mutex // Like TCPListener, serialize accepts, but never Close.
	mu       sync.Mutex // Protect mode, lazy initialization, and Close.
	fallback bool
	closed   bool
	file     *os.File
	raw      syscall.RawConn
}

func wrapListener(listener net.Listener) net.Listener {
	tcp, ok := listener.(*net.TCPListener)
	if !ok {
		return listener
	}
	return &tcpListener{tcp: tcp, accept: tcp.Accept}
}

func (l *tcpListener) Addr() net.Addr { return l.tcp.Addr() }

func (l *tcpListener) Accept() (net.Conn, error) {
	defer rawlog.Begin("listener Accept " + l.Addr().String())()
	l.acceptMu.Lock()
	defer l.acceptMu.Unlock()
	l.mu.Lock()
	fallback, closed := l.fallback, l.closed
	l.mu.Unlock()
	if closed {
		return nil, l.acceptError(net.ErrClosed)
	}
	if !fallback {
		conn, err := l.accept()
		var call *os.SyscallError
		if !errors.As(err, &call) || call.Syscall != "accept4" || !errors.Is(call.Err, syscall.ENOSYS) {
			return conn, err
		}
		l.mu.Lock()
		l.fallback = true
		rawlog.Log("accept4 ENOSYS: entering fallback address=%s", l.Addr())
		l.mu.Unlock()
	}
	raw, err := l.fallbackConn()
	if err != nil {
		return nil, l.acceptError(err)
	}
	fd := -1
	var acceptErr error
	waitRead := rawlog.Begin("listener poll/legacy accept " + l.Addr().String())
	err = raw.Read(func(listenerFd uintptr) bool {
		flags, flagErr := unix.FcntlInt(listenerFd, unix.F_GETFL, 0)
		rawlog.Log("listener fd=%d flags=%#x error=%s", listenerFd, flags, rawlog.Error(flagErr))
		for {
			fd, acceptErr = acceptCloseOnExec(int(listenerFd))
			switch acceptErr {
			case syscall.EINTR, syscall.ECONNABORTED:
				continue
			case syscall.EAGAIN:
				return false // Let golang wait for readiness without holding ForkLock.
			}
			return true
		}
	})
	waitRead()
	rawlog.Log("legacy accept result fd=%d poll_error=%s accept_error=%s", fd, rawlog.Error(err), rawlog.Error(acceptErr))
	if err != nil {
		// RawConn can expose an internal closed-file error. Identify closure
		// from our state instead of depending on internal error strings.
		l.mu.Lock()
		if l.closed {
			err = net.ErrClosed
		}
		l.mu.Unlock()
		return nil, l.acceptError(err)
	}
	if acceptErr != nil {
		return nil, l.acceptError(acceptErr)
	}
	conn, err := acceptedConn(fd)
	if err != nil {
		return nil, l.acceptError(err)
	}
	return conn, nil
}

func (l *tcpListener) fallbackConn() (syscall.RawConn, error) {
	defer rawlog.Begin("listener fallback initialization")()
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil, net.ErrClosed
	}
	if l.raw != nil {
		return l.raw, nil
	}
	original, err := l.tcp.SyscallConn()
	if err != nil {
		return nil, err
	}
	fd := -1
	var dupErr error
	err = original.Control(func(socket uintptr) {
		fd, dupErr = unix.FcntlInt(socket, unix.F_DUPFD_CLOEXEC, 0)
	})
	if err != nil {
		return nil, err
	}
	if dupErr != nil {
		return nil, os.NewSyscallError("fcntl", dupErr)
	}
	// iSH's epoll keys registrations by socket, not descriptor. Unregister
	// the TCPListener before registering its duplicate. The duplicate keeps
	// the socket and its pending connections alive throughout the handoff.
	if err := l.tcp.Close(); err != nil {
		unix.Close(fd)
		l.closed = true
		return nil, err
	}
	file := os.NewFile(uintptr(fd), "TCP listener")
	raw, err := file.SyscallConn()
	if err == nil {
		err = file.SetReadDeadline(time.Time{}) // Verify golang could register it for polling.
	}
	if err != nil {
		file.Close()
		l.closed = true
		return nil, err
	}
	l.file, l.raw = file, raw
	return raw, nil
}

func (l *tcpListener) Close() error {
	defer rawlog.Begin("listener Close " + l.Addr().String())()
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return net.ErrClosed
	}
	l.closed = true
	file := l.file
	l.mu.Unlock()
	if file != nil {
		return file.Close() // Wake RawConn.Read; the original listener is already closed.
	}
	return l.tcp.Close()
}

func (l *tcpListener) acceptError(err error) error {
	if errors.Is(err, os.ErrClosed) {
		err = net.ErrClosed
	}
	return &net.OpError{Op: "accept", Net: l.Addr().Network(), Addr: l.Addr(), Err: err}
}

// acceptedConn takes ownership of fd on both success and failure.
func acceptedConn(fd int) (net.Conn, error) {
	defer rawlog.Begin("listener accepted socket conversion")()
	flags, flagErr := unix.FcntlInt(uintptr(fd), unix.F_GETFL, 0)
	rawlog.Log("accepted fd=%d flags=%#x error=%s", fd, flags, rawlog.Error(flagErr))
	// iSH's Darwin host can inherit NONBLOCK on accept. Keep the temporary
	// os.File unpolled: FileConn registers its own duplicate, and iSH rejects
	// two poll registrations for the same socket. FileConn restores NONBLOCK.
	if err := syscall.SetNonblock(fd, false); err != nil {
		unix.Close(fd)
		return nil, os.NewSyscallError("setnonblock", err)
	}
	file := os.NewFile(uintptr(fd), "accepted TCP socket")
	defer file.Close()
	return net.FileConn(file)
}

func acceptCloseOnExec(listenerFd int) (int, error) {
	// The listener is nonblocking, so this lock is held only for syscalls,
	// never while waiting for a connection. Match golang's fork/exec protection.
	syscall.ForkLock.RLock()
	defer syscall.ForkLock.RUnlock()
	fd, err := acceptSocket(listenerFd)
	if err != nil {
		return -1, err
	}
	for {
		_, err = unix.FcntlInt(uintptr(fd), unix.F_SETFD, unix.FD_CLOEXEC)
		if err != syscall.EINTR {
			break
		}
	}
	if err != nil {
		unix.Close(fd)
		return -1, os.NewSyscallError("fcntl", err)
	}
	return fd, nil
}
