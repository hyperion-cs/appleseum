package rawlog

import (
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestRawFileCapturesStderrWithoutRequestSecrets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "debug.log")
	if err := os.WriteFile(path, []byte("previous run marker\n"), 0600); err != nil {
		t.Fatal(err)
	}
	closeLog, err := Start(path)
	if err != nil {
		t.Fatal(err)
	}
	defer closeLog()
	marker := "credential-must-not-be-logged"
	r := httptest.NewRequest("POST", "/api/"+marker+"?token="+marker, strings.NewReader(marker))
	r.Header.Set("Authorization", marker)
	Handler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })).ServeHTTP(httptest.NewRecorder(), r)
	Log("error=%s", Error(&url.Error{Op: "Post", URL: "https://" + marker, Err: errors.New(marker)}))
	log.Print("stderr capture marker")
	if err := closeLog(); err != nil {
		t.Fatal(err)
	}
	text, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(text), marker) {
		t.Fatal("request or error secret leaked")
	}
	for _, want := range []string{"previous run marker", "HTTP POST /api/<other>", "raw logging started", "stderr capture marker"} {
		if !strings.Contains(string(text), want) {
			t.Fatalf("missing %q", want)
		}
	}
	if Enabled() {
		t.Fatal("raw logger remained active after close")
	}
}

func TestRuntimePanicIsCapturedWithoutShellRedirection(t *testing.T) {
	if path := os.Getenv("APPLESEUM_RAW_PANIC_TEST"); path != "" {
		if _, err := Start(path); err != nil {
			panic(err)
		}
		if os.Getenv("APPLESEUM_RAW_PANIC_REOPEN_TEST") == "1" {
			if err := os.Remove(path); err != nil {
				panic(err)
			}
			Log("reopen before runtime crash")
		}
		panic("raw runtime crash marker")
	}
	for _, mode := range []string{"initial", "recreated"} {
		t.Run(mode, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "debug.log")
			args := []string{os.Args[0], "-test.run=^TestRuntimePanicIsCapturedWithoutShellRedirection$"}
			if emulator := os.Getenv("APPLESEUM_TEST_EXEC"); emulator != "" {
				args = append([]string{emulator}, args...)
			}
			command := exec.Command(args[0], args[1:]...)
			command.Env = append(os.Environ(), "APPLESEUM_RAW_PANIC_TEST="+path, "APPLESEUM_RAW_PANIC_REOPEN_TEST=0")
			if mode == "recreated" {
				command.Env = append(command.Env, "APPLESEUM_RAW_PANIC_REOPEN_TEST=1")
			}
			if err := command.Run(); err == nil {
				t.Fatal("helper did not panic")
			}
			text, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(text), "panic: raw runtime crash marker") || !strings.Contains(string(text), "goroutine") {
				t.Fatalf("runtime crash missing from file: %s", text)
			}
		})
	}
}

func TestRawLogRecoversExternalFileChanges(t *testing.T) {
	for _, mode := range []string{"remove", "replace", "truncate"} {
		t.Run(mode, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "debug.log")
			closeLog, err := Start(path)
			if err != nil {
				t.Fatal(err)
			}
			defer closeLog()
			if err := os.WriteFile(path+".1", []byte("retained backup"), 0600); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "remove":
				err = os.Remove(path)
			case "replace":
				err = os.Rename(path, path+".previous")
				if err == nil {
					err = os.WriteFile(path, []byte("replacement prefix\n"), 0600)
				}
			case "truncate":
				var file *os.File
				file, err = os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0600)
				if err == nil {
					err = file.Close()
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			Log("record after external change")
			log.Print("stderr after external change")
			if err := closeLog(); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			text := string(data)
			if !strings.Contains(text, "record after external change") || !strings.Contains(text, "stderr after external change") || strings.ContainsRune(text, 0) {
				t.Fatalf("log or stderr was lost after %s: %q", mode, text)
			}
			if mode == "replace" && !strings.HasPrefix(text, "replacement prefix\n") {
				t.Fatal("replacement file was overwritten")
			}
			backup, err := os.ReadFile(path + ".1")
			if err != nil || string(backup) != "retained backup" {
				t.Fatalf("reopening altered rotation backups: %q %v", backup, err)
			}
		})
	}
}

func TestRawLogRetriesFailedReopenAndKeepsUnchangedFileOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "debug.log")
	f, err := openLog(path)
	if err != nil {
		t.Fatal(err)
	}
	w := &rotatingFile{path: path, file: f, limit: 4_096}
	defer func() {
		if w.file != nil {
			w.file.Close()
		}
	}()
	if _, err := w.Write([]byte("initial\n")); err != nil || w.file != f {
		t.Fatalf("unchanged log was reopened: %v", err)
	}
	if err := os.Rename(path, path+".previous"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("blocked\n")); err == nil {
		t.Fatal("unwritable replacement was accepted")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("recovered\n")); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "recovered\n" {
		t.Fatalf("failed reopen did not recover: %q %v", data, err)
	}
}

func TestClosedRawLogDoesNotRecreateFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "debug.log")
	closeLog, err := Start(path)
	if err != nil {
		t.Fatal(err)
	}
	r := current.Load()
	if err := closeLog(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := r.output.Write([]byte("late record\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("closed logger recreated its file: %v", err)
	}
}

func TestStderrFollowsRotation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "debug.log")
	closeLog, err := Start(path)
	if err != nil {
		t.Fatal(err)
	}
	defer closeLog()
	r := current.Load()
	r.output.mu.Lock()
	r.output.limit = 4_096
	r.output.mu.Unlock()
	for range 100 {
		Log("rotation record %s", strings.Repeat("x", 200))
	}
	log.Print("stderr after rotation marker")
	closeLog()
	text, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(text), "stderr after rotation marker") {
		t.Fatal("stderr kept writing to a rotated or closed file")
	}
}

func TestRotationRetainsBoundedRecentLogsUnderConcurrentWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "debug.log")
	f, err := openLog(path)
	if err != nil {
		t.Fatal(err)
	}
	w := &rotatingFile{path: path, file: f, limit: 256}
	defer func() { w.file.Close() }()
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			for range 40 {
				if _, err := w.Write([]byte(strings.Repeat("x", 60) + "\n")); err != nil {
					t.Error(err)
				}
			}
		})
	}
	wg.Wait()
	if _, err := w.Write([]byte("last record\n")); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != backups+1 {
		t.Fatalf("got %d log files", len(entries))
	}
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			t.Fatal(err)
		}
		if info.Size() > w.limit {
			t.Fatalf("%s exceeds log limit", entry.Name())
		}
	}
	text, _ := os.ReadFile(path)
	if !strings.HasSuffix(string(text), "last record\n") {
		t.Fatal("latest record lost during rotation")
	}
}
