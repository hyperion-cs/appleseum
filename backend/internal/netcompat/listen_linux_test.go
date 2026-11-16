package netcompat

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func testListener(t *testing.T, network string) *tcpListener {
	t.Helper()
	address := "127.0.0.1:0"
	if network == "tcp6" {
		address = "[::1]:0"
	}
	listener, err := Listen(network, address)
	if err != nil {
		if network == "tcp6" {
			t.Skipf("IPv6 unavailable: %v", err)
		}
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	return listener.(*tcpListener)
}

// Override only this listener's standard accept. The fallback exercises actual
// sockets, legacy accept syscalls, golang's poller, and net.FileConn.
func forceNoAccept4(listener *tcpListener) *atomic.Int32 {
	var calls atomic.Int32

	listener.accept = func() (net.Conn, error) {
		calls.Add(1)
		return nil, &net.OpError{Op: "accept", Net: "tcp", Err: os.NewSyscallError("accept4", syscall.ENOSYS)}
	}

	return &calls
}

func waitForFallback(t *testing.T, listener *tcpListener) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		listener.mu.Lock()
		ready := listener.file != nil
		listener.mu.Unlock()
		if ready {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("fallback was not initialized")
}

func socketFlags(t *testing.T, conn syscall.Conn) (status, descriptor int) {
	t.Helper()
	raw, err := conn.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	var callErr error
	err = raw.Control(func(fd uintptr) {
		status, callErr = unix.FcntlInt(fd, unix.F_GETFL, 0)
		if callErr == nil {
			descriptor, callErr = unix.FcntlInt(fd, unix.F_GETFD, 0)
		}
	})
	if err != nil || callErr != nil {
		t.Fatal(err, callErr)
	}
	return
}

func TestStandardAcceptDoesNotInitializeFallback(t *testing.T) {
	listener := testListener(t, "tcp4")
	result := startAccept(listener)
	client, err := net.DialTimeout("tcp", listener.Addr().String(), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	got := receiveAccept(t, result)
	if got.err != nil {
		t.Fatal(got.err)
	}
	got.conn.Close()
	if listener.fallback || listener.file != nil {
		t.Fatal("normal accept initialized fallback resources")
	}
}

func TestFallbackTcp(t *testing.T) {
	for _, network := range []string{"tcp4", "tcp6"} {
		t.Run(network, func(t *testing.T) {
			listener := testListener(t, network)
			beforeStatus, beforeDescriptor := socketFlags(t, listener.tcp)
			calls := forceNoAccept4(listener)
			for i := range 3 {
				result := startAccept(listener)
				client, err := net.DialTimeout("tcp", listener.Addr().String(), 5*time.Second)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { client.Close() })
				got := receiveAccept(t, result)
				if got.err != nil {
					t.Fatal(got.err)
				}
				t.Cleanup(func() { got.conn.Close() })
				conn, ok := got.conn.(*net.TCPConn)
				if !ok {
					t.Fatalf("expected TCPConn, got %T", got.conn)
				}
				if conn.RemoteAddr().String() != client.LocalAddr().String() || conn.LocalAddr().String() != client.RemoteAddr().String() {
					t.Fatal("connection addresses changed")
				}
				status, descriptor := socketFlags(t, conn)
				if status&unix.O_NONBLOCK == 0 || descriptor&unix.FD_CLOEXEC == 0 {
					t.Fatal("accepted socket missing NONBLOCK or CLOEXEC")
				}
				conn.SetReadDeadline(time.Now().Add(20 * time.Millisecond))
				var data [1]byte
				_, err = conn.Read(data[:])
				var timeout net.Error
				if !errors.As(err, &timeout) || !timeout.Timeout() {
					t.Fatalf("read deadline was not honored: %v", err)
				}
				conn.SetDeadline(time.Now().Add(5 * time.Second))
				client.SetDeadline(time.Now().Add(5 * time.Second))
				if i == 2 {
					afterStatus, afterDescriptor := socketFlags(t, listener.file)
					if afterStatus != beforeStatus || afterDescriptor != beforeDescriptor {
						t.Fatal("handoff did not preserve listener flags")
					}
					if _, err := listener.tcp.Accept(); !errors.Is(err, net.ErrClosed) {
						t.Fatalf("original listener was not closed during handoff: %v", err)
					}
					if err := listener.Close(); err != nil {
						t.Fatal(err)
					}
				}
				// Even closing the listener must leave accepted connections usable.
				if _, err := client.Write([]byte("a")); err != nil {
					t.Fatal(err)
				}
				if _, err := io.ReadFull(conn, data[:]); err != nil || data[0] != 'a' {
					t.Fatalf("read: %q, %v", data, err)
				}
				if _, err := conn.Write([]byte("b")); err != nil {
					t.Fatal(err)
				}
				if _, err := io.ReadFull(client, data[:]); err != nil || data[0] != 'b' {
					t.Fatalf("reply: %q, %v", data, err)
				}
				conn.Close()
				client.Close()
			}
			if calls.Load() != 1 {
				t.Fatalf("unsupported accept4 called %d times", calls.Load())
			}
		})
	}
}

func TestOtherErrorsDoNotEnableFallback(t *testing.T) {
	for _, err := range []error{
		os.NewSyscallError("accept4", syscall.EPERM),
		os.NewSyscallError("accept4", syscall.EINVAL),
		os.NewSyscallError("accept4", syscall.EMFILE),
		os.NewSyscallError("getsockname", syscall.ENOSYS),
		syscall.ENOSYS,
	} {
		t.Run(err.Error(), func(t *testing.T) {
			listener := testListener(t, "tcp4")

			listener.accept = func() (net.Conn, error) { return nil, err }

			conn, got := listener.Accept()
			if conn != nil || got != err || listener.fallback || listener.file != nil {
				t.Fatalf("unexpected fallback or changed error: %v", got)
			}
		})
	}
}

func TestAcceptedSocketInheritingNonblock(t *testing.T) {
	listener := testListener(t, "tcp4")
	client, err := net.DialTimeout("tcp", listener.Addr().String(), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	raw, err := listener.tcp.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	fd := -1
	var acceptErr error
	if err := raw.Control(func(socket uintptr) {
		fd, acceptErr = acceptCloseOnExec(int(socket))
	}); err != nil || acceptErr != nil {
		t.Fatal(err, acceptErr)
	}
	// Reproduce BSD accept's inherited flags on an ordinary Linux host.
	if err := syscall.SetNonblock(fd, true); err != nil {
		unix.Close(fd)
		t.Fatal(err)
	}
	conn, err := acceptedConn(fd)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0); err != syscall.EBADF {
		t.Fatal("temporary accepted descriptor was not closed")
	}
	status, descriptor := socketFlags(t, conn.(*net.TCPConn))
	if status&unix.O_NONBLOCK == 0 || descriptor&unix.FD_CLOEXEC == 0 {
		t.Fatal("FileConn did not configure its socket")
	}
}

func TestCloseUnblocksConcurrentAccepts(t *testing.T) {
	for _, fallback := range []bool{false, true} {
		t.Run(fmt.Sprint(fallback), func(t *testing.T) {
			listener := testListener(t, "tcp4")
			if fallback {
				forceNoAccept4(listener)
			}
			var pending []<-chan acceptResult
			for range 8 {
				pending = append(pending, startAccept(listener))
			}
			if fallback {
				waitForFallback(t, listener)
			}
			if err := listener.Close(); err != nil {
				t.Fatal(err)
			}
			for _, result := range pending {
				got := receiveAccept(t, result)
				if got.conn != nil || !errors.Is(got.err, net.ErrClosed) {
					t.Fatalf("Accept after close: %v", got.err)
				}
			}
			if !errors.Is(listener.Close(), net.ErrClosed) {
				t.Fatal("repeated Close did not report closed listener")
			}
		})
	}
}

func TestCloseDuringFallbackTransition(t *testing.T) {
	listener := testListener(t, "tcp4")
	entered, release := make(chan struct{}), make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	defer unblock()

	listener.accept = func() (net.Conn, error) {
		close(entered)
		<-release
		return nil, os.NewSyscallError("accept4", syscall.ENOSYS)
	}

	result := startAccept(listener)
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("Accept did not start")
	}
	closed := make(chan error, 1)
	go func() { closed <- listener.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close blocked on Accept")
	}
	unblock()
	got := receiveAccept(t, result)
	if !errors.Is(got.err, net.ErrClosed) || listener.file != nil {
		t.Fatalf("fallback initialized after Close: %v", got.err)
	}
}

func TestConcurrentFallbackAccepts(t *testing.T) {
	listener := testListener(t, "tcp4")
	calls := forceNoAccept4(listener)
	var pending []<-chan acceptResult
	for range 8 {
		pending = append(pending, startAccept(listener))
		client, err := net.DialTimeout("tcp", listener.Addr().String(), 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { client.Close() })
	}
	for _, result := range pending {
		got := receiveAccept(t, result)
		if got.err != nil {
			t.Fatal(got.err)
		}
		got.conn.Close()
	}
	if calls.Load() != 1 {
		t.Fatalf("accept4 attempted %d times", calls.Load())
	}
}

func TestFallbackHttpAndHttps(t *testing.T) {
	for _, tls := range []bool{false, true} {
		t.Run(fmt.Sprint(tls), func(t *testing.T) {
			listener := testListener(t, "tcp4")
			calls := forceNoAccept4(listener)
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				io.WriteString(w, "ok")
			}))
			server.Listener.Close()
			server.Listener = listener
			if tls {
				server.StartTLS()
			} else {
				server.Start()
			}
			defer server.Close()
			client := server.Client()
			client.Timeout = 5 * time.Second
			for range 3 {
				response, err := client.Get(server.URL)
				if err != nil {
					t.Fatal(err)
				}
				body, err := io.ReadAll(response.Body)
				response.Body.Close()
				client.CloseIdleConnections()
				if err != nil || response.StatusCode != 200 || string(body) != "ok" {
					t.Fatalf("HTTP response %q, %v", body, err)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := server.Config.Shutdown(ctx); err != nil {
				t.Fatal(err)
			}
			if calls.Load() != 1 {
				t.Fatal("accept4 retried after fallback")
			}
		})
	}
}

func TestFallbackInitializationFailure(t *testing.T) {
	listener := testListener(t, "tcp4")
	calls := forceNoAccept4(listener)
	listener.tcp.Close() // File duplication will fail.
	for range 2 {
		if conn, err := listener.Accept(); conn != nil || err == nil {
			t.Fatal("initialization failure hidden")
		}
	}
	if calls.Load() != 1 || listener.file != nil {
		t.Fatal("failed initialization retried accept4 or retained a file")
	}
}

func TestFallbackDoesNotLeakDescriptors(t *testing.T) {
	// Initialize golang's process-wide poller before counting descriptors.
	warm := testListener(t, "tcp4")
	warm.Close()

	count := func() int {
		entries, err := os.ReadDir("/proc/self/fd")
		if err != nil {
			t.Skip(err)
		}
		return len(entries)
	}

	before := count()
	for range 20 {
		func() {
			listener, err := Listen("tcp4", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			forceNoAccept4(listener.(*tcpListener))
			client, err := net.DialTimeout("tcp", listener.Addr().String(), 5*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			conn, err := listener.Accept()
			if err != nil {
				t.Fatal(err)
			}
			conn.Close()
		}()
	}
	if after := count(); after != before {
		t.Fatalf("descriptor count grew from %d to %d", before, after)
	}
}

func TestNonTcpListenerIsUnchanged(t *testing.T) {
	listener, err := net.Listen("unix", filepath.Join(t.TempDir(), "socket"))
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if wrapListener(listener) != listener {
		t.Fatal("non-TCP listener was wrapped")
	}
}
