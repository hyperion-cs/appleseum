// Package crashreport keeps a short console notification available when the
// application's golang runtime can no longer execute ordinary handlers.
package crashreport

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"runtime/debug"
	"syscall"
	"time"
)

const (
	monitorArgument = "--internal-crash-monitor"
	message         = "Critical error; exiting."
)

type monitor struct {
	kill func() error
	done chan struct{}
}

// Start and Stop belong to the application's main goroutine.
var active *monitor

// RunMonitor handles the private child mode before application initialization.
func RunMonitor() bool {
	if len(os.Args) < 2 || len(os.Args) > 3 || os.Args[1] != monitorArgument || len(os.Args) == 3 && os.Args[2] != "raw" {
		return false
	}
	signal.Ignore(os.Interrupt, syscall.SIGTERM)
	if n, _ := io.CopyN(io.Discard, os.Stdin, 1); n != 0 {
		details := "Use --debug=raw for details."
		if len(os.Args) == 3 {
			details = "Details: debug.log."
		}
		fmt.Fprintln(os.Stdout, message, details)
		// Drain without retaining the report. The runtime writes its full copy
		// to stderr, which the application may have redirected to the raw log.
		io.Copy(io.Discard, os.Stdin)
	}
	return true
}

// Start enables notifications before logging or server initialization. A child
// is necessary because fatal runtime errors can stop every application thread.
func Start(ish, raw bool) error {
	path, err := os.Executable()
	if err == nil {
		err = start(exec.Command(path, monitorArgs(raw)...), ish)
	}
	if err == nil {
		return nil
	}
	// Restricted environments may forbid another process. Preserve visible
	// crash details there even when stderr is redirected to a raw log.
	if fallback := debug.SetCrashOutput(os.Stdout, debug.CrashOptions{}); fallback != nil {
		return errors.Join(err, fallback)
	}
	fmt.Fprintln(os.Stdout, "Could not start crash notifications; crash details will be printed to the console.")
	return nil
}

func monitorArgs(raw bool) []string {
	args := []string{monitorArgument}
	if raw {
		args = append(args, "raw")
	}
	return args
}

func start(command *exec.Cmd, ish bool) error {
	input, output, err := os.Pipe()
	if err != nil {
		return err
	}
	defer input.Close()
	defer output.Close()
	command.Stdin, command.Stdout, command.Stderr = input, os.Stdout, os.Stderr
	// The idle monitor needs one processor and must remain safe inside iSH.
	// Keep platform environment variables while excluding application tuning.
	command.Env = append(os.Environ(), "GOMAXPROCS=1", "GODEBUG=madvdontneed=0,asyncpreemptoff=1", "GOGC=100", "GOMEMLIMIT=off")
	m, err := launch(command, input, ish)
	if err != nil {
		return err
	}
	if err := debug.SetCrashOutput(output, debug.CrashOptions{}); err != nil {
		if m.kill != nil {
			m.kill()
		}
		return err
	}
	active = m
	return nil
}

func startCommand(command *exec.Cmd) (*monitor, error) {
	if err := command.Start(); err != nil {
		return nil, err
	}
	m := &monitor{kill: command.Process.Kill, done: make(chan struct{})}
	go func() { command.Wait(); close(m.done) }()
	return m, nil
}

// Stop releases the monitor after an ordinary return from the application.
// Do not defer it: unwinding a panic must leave crash reporting enabled.
func Stop() {
	debug.SetCrashOutput(nil, debug.CrashOptions{})
	if m := active; m != nil {
		active = nil
		select {
		case <-m.done:
		case <-time.After(2 * time.Second):
			if m.kill != nil {
				m.kill()
			}
		}
	}
}
