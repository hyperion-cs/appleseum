//go:build !windows

package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"testing"
	"time"
)

type blockedCloseListener struct {
	net.Listener
	accepting chan struct{}
	once      sync.Once
}

func (l *blockedCloseListener) Accept() (net.Conn, error) {
	l.once.Do(func() { close(l.accepting) })
	return l.Listener.Accept()
}

func (l *blockedCloseListener) Close() error {
	fmt.Println("listener-close-blocked")
	select {}
}

func shutdownChild(mode string) {
	timeout := 200 * time.Millisecond
	if mode == "second-signal" {
		timeout = 5 * time.Second
	}
	ctx, begin, finish := shutdownSignals(timeout, os.Exit)
	defer finish() // Keep the watchdog alive throughout every later defer.
	var server *http.Server
	if mode == "blocked-listener" {
		listener, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			panic(err)
		}
		blocked := &blockedCloseListener{Listener: listener, accepting: make(chan struct{})}
		server = &http.Server{}
		go server.Serve(blocked)
		<-blocked.accepting
	}
	fmt.Println("ready")
	<-ctx.Done()
	begin()
	fmt.Println("stopping")
	switch mode {
	case "blocked-listener":
		deadline, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		defer cancel()
		server.Shutdown(deadline)
	case "blocked-defer", "second-signal":
		defer func() { fmt.Println("cleanup-blocked"); select {} }()
	}
}

func TestShutdownChild(t *testing.T) {
	mode := os.Getenv("APPLESEUM_SHUTDOWN_TEST")
	if mode == "" {
		return
	}
	shutdownChild(mode)
	// A completed shutdown must disarm its watchdog.
	time.Sleep(300 * time.Millisecond)
	os.Exit(0)
}

func TestShutdownSignalsAndDeadlineWithBlockedCleanup(t *testing.T) {
	for _, tc := range []struct {
		name, mode, blocked string
		signal              os.Signal
		second              bool
		exitCode            int
	}{
		{"graceful", "graceful", "", os.Interrupt, false, 0},
		{"blocked listener", "blocked-listener", "listener-close-blocked", os.Interrupt, false, 1},
		{"blocked defer", "blocked-defer", "cleanup-blocked", os.Interrupt, false, 1},
		{"second interrupt", "second-signal", "cleanup-blocked", os.Interrupt, true, 130},
		{"second terminate", "second-signal", "cleanup-blocked", syscall.SIGTERM, true, 143},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer cancel()
			args := []string{os.Args[0], "-test.run=^TestShutdownChild$"}
			if emulator := os.Getenv("APPLESEUM_TEST_EXEC"); emulator != "" {
				args = append([]string{emulator}, args...)
			}
			command := exec.CommandContext(ctx, args[0], args[1:]...)
			command.Env = append(os.Environ(), "APPLESEUM_SHUTDOWN_TEST="+tc.mode)
			var stderr bytes.Buffer
			command.Stderr = &stderr
			stdout, err := command.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { command.Process.Kill() })
			lines := make(chan string, 16)
			go func() {
				defer close(lines)
				scanner := bufio.NewScanner(stdout)
				for scanner.Scan() {
					lines <- scanner.Text()
				}
			}()

			waitLine := func(want string) {
				t.Helper()
				for {
					select {
					case line, ok := <-lines:
						if !ok {
							t.Fatalf("process ended before %q", want)
						}
						if line == want {
							return
						}
					case <-ctx.Done():
						t.Fatalf("waiting for %q: %v", want, ctx.Err())
					}
				}
			}

			waitLine("ready")
			if err := command.Process.Signal(tc.signal); err != nil {
				t.Fatal(err)
			}
			waitLine("stopping")
			if tc.blocked != "" {
				waitLine(tc.blocked)
			}
			if tc.second {
				if err := command.Process.Signal(tc.signal); err != nil {
					t.Fatal(err)
				}
			}
			err = command.Wait()
			if command.ProcessState.ExitCode() != tc.exitCode || ctx.Err() != nil {
				t.Fatalf("exit=%d, want %d; error=%v context=%v stderr=%s", command.ProcessState.ExitCode(), tc.exitCode, err, ctx.Err(), stderr.String())
			}
		})
	}
}
