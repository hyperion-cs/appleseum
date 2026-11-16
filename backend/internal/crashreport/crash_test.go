package crashreport

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"appleseum/backend/internal/rawlog"
)

func TestMain(m *testing.M) {
	if RunMonitor() {
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestCrashProcess(t *testing.T) {
	mode := os.Getenv("APPLESEUM_CRASH_TEST")
	if mode == "" {
		return
	}
	var err error
	ish := os.Getenv("APPLESEUM_CRASH_TEST_PLATFORM") == "ish"
	path := os.Getenv("APPLESEUM_CRASH_TEST_LOG")
	if emulator := os.Getenv("APPLESEUM_TEST_EXEC"); emulator != "" {
		err = start(exec.Command(emulator, append([]string{os.Args[0]}, monitorArgs(path != "")...)...), ish)
	} else {
		err = Start(ish, path != "")
	}
	if err != nil {
		t.Fatal(err)
	}
	if path != "" {
		if _, err := rawlog.Start(path); err != nil {
			t.Fatal(err)
		}
	}
	switch mode {
	case "panic":
		panic("unhandled crash marker")
	case "goroutine":
		go func() { panic("background crash marker") }()
		select {}
	case "fatal":
		// This is a runtime fatal error; recover cannot handle it.
		var mu sync.Mutex
		mu.Unlock()
	case "stderr":
		fmt.Fprintln(os.Stderr, "ordinary diagnostic marker")
	case "normal":
	default:
		t.Fatal("unknown helper mode")
	}
	// Only ordinary returns close the log and the monitor, as in main.
	if err := rawlog.Close(); err != nil {
		t.Fatal(err)
	}
	m := active
	Stop()
	select {
	case <-m.done:
	default:
		t.Fatal("monitor remained running after normal shutdown")
	}
}

func TestConsoleNotificationForUnhandledPanicsAndRuntimeFatals(t *testing.T) {
	for _, raw := range []bool{false, true} {
		for _, mode := range []string{"normal", "stderr", "panic", "goroutine", "fatal"} {
			t.Run(fmt.Sprintf("raw=%t/%s", raw, mode), func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				args := []string{os.Args[0], "-test.run=^TestCrashProcess$"}
				if emulator := os.Getenv("APPLESEUM_TEST_EXEC"); emulator != "" {
					args = append([]string{emulator}, args...)
				}
				command := exec.CommandContext(ctx, args[0], args[1:]...)
				command.Env = append(os.Environ(), "APPLESEUM_CRASH_TEST="+mode, "APPLESEUM_CRASH_TEST_LOG=")
				path := filepath.Join(t.TempDir(), "debug.log")
				if raw {
					command.Env = append(command.Env, "APPLESEUM_CRASH_TEST_LOG="+path)
				}
				var stdout, stderr bytes.Buffer
				command.Stdout, command.Stderr = &stdout, &stderr
				err := command.Run()
				crashed := mode == "panic" || mode == "goroutine" || mode == "fatal"
				if ctx.Err() != nil || (err != nil) != crashed {
					t.Fatalf("exit=%v context=%v stdout=%s stderr=%s", err, ctx.Err(), &stdout, &stderr)
				}
				want := 0
				if crashed {
					want = 1
				}
				if strings.Count(stdout.String(), message) != want {
					t.Fatalf("console notification count: want=%d stdout=%s stderr=%s", want, &stdout, &stderr)
				}
				if crashed {
					hint := "Use --debug=raw for details."
					if raw {
						hint = "Details: debug.log."
					}
					if !strings.Contains(stdout.String(), hint) {
						t.Fatalf("missing crash hint %q: %s", hint, &stdout)
					}
				}
				details := stderr.String()
				if raw {
					data, readErr := os.ReadFile(path)
					if readErr != nil {
						t.Fatal(readErr)
					}
					details = string(data)
					if strings.Contains(stderr.String(), "goroutine") {
						t.Fatal("raw crash report leaked to the console")
					}
				}
				if crashed && !strings.Contains(details, "goroutine") {
					t.Fatalf("full crash report missing: %s", details)
				}
				if mode == "fatal" && !strings.Contains(details, "fatal error: sync: unlock of unlocked mutex") {
					t.Fatalf("runtime fatal was not exercised: %s", details)
				}
			})
		}
	}
}
