package main

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDefaultDataDirectory(t *testing.T) {
	home := filepath.Join(t.TempDir(), "user")
	local := filepath.Join(home, "AppData", "Local")
	xdg := filepath.Join(t.TempDir(), "shared-data")
	for _, tc := range []struct {
		name, platform, home, local, xdg, want string
	}{
		{"Linux", "linux", home, "", "", filepath.Join(home, ".local", "share", "appleseum")},
		{"Android", "android", home, "", "", filepath.Join(home, ".local", "share", "appleseum")},
		{"macOS", "darwin", home, "", xdg, filepath.Join(home, "Library", "Application Support", "Appleseum")},
		{"Windows", "windows", home, local, xdg, filepath.Join(local, "Appleseum", "data")},
		{"Linux XDG", "linux", "", "", xdg, filepath.Join(xdg, "appleseum")},
		{"Android XDG", "android", home, "", xdg, filepath.Join(xdg, "appleseum")},
		{"relative XDG ignored", "linux", home, "", "relative", filepath.Join(home, ".local", "share", "appleseum")},
		{"missing HOME", "linux", "", "", "", ""},
		{"relative HOME", "linux", "relative", "", "", ""},
		{"missing macOS HOME", "darwin", "", "", xdg, ""},
		{"missing LOCALAPPDATA", "windows", home, "", xdg, ""},
		{"relative LOCALAPPDATA", "windows", home, "relative", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := map[string]string{"HOME": tc.home, "LOCALAPPDATA": tc.local, "XDG_DATA_HOME": tc.xdg}
			got, err := defaultDataDirectory(tc.platform, func(name string) string { return env[name] })
			if tc.want == "" {
				if err == nil || !strings.Contains(err.Error(), "--data") {
					t.Fatalf("expected an actionable error, got %q, %v", got, err)
				}
			} else if err != nil || got != tc.want {
				t.Fatalf("got %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}

func TestDataDirectory(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	t.Setenv("HOME", "")
	t.Setenv("LOCALAPPDATA", "")
	t.Setenv("XDG_DATA_HOME", "")
	want := filepath.Join(root, "custom", "nested", "library")
	for _, input := range []string{filepath.Join("custom", "nested", "library"), want} {
		got, err := dataDirectory(input)
		if err != nil || got != want {
			t.Fatalf("explicit path: %q, %v", got, err)
		}
		if info, err := os.Stat(filepath.Join(want, "packages")); err != nil || !info.IsDir() {
			t.Fatalf("missing directory tree: %v", err)
		}
	}
	marker := filepath.Join(want, "existing")
	if err := os.WriteFile(marker, []byte("synthetic library data"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := dataDirectory(want); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(marker); err != nil || string(data) != "synthetic library data" {
		t.Fatal("existing data changed", err)
	}
	blocked := filepath.Join(root, "blocked")
	if err := os.WriteFile(blocked, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := dataDirectory(blocked); err == nil || !strings.Contains(err.Error(), blocked) {
		t.Fatal("expected an error with the data path", err)
	}
	if data, err := os.ReadFile(blocked); err != nil || string(data) != "keep" {
		t.Fatal("blocked file changed", err)
	}
}

func TestDataDirectoryIndependentOfWorkingDirectory(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	t.Setenv("HOME", home)
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "xdg"))
	var first string
	for _, name := range []string{"first", "second"} {
		cwd := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Join(cwd, "data"), 0700); err != nil {
			t.Fatal(err)
		}
		marker := filepath.Join(cwd, "data", "existing")
		if err := os.WriteFile(marker, []byte("legacy data"), 0600); err != nil {
			t.Fatal(err)
		}
		t.Chdir(cwd)
		got, err := dataDirectory("")
		if err != nil || !filepath.IsAbs(got) || got == filepath.Join(cwd, "data") {
			t.Fatalf("default path: %q, %v", got, err)
		}
		if first != "" && got != first {
			t.Fatalf("default changed with cwd: %q -> %q", first, got)
		}
		first = got
		if data, err := os.ReadFile(marker); err != nil || string(data) != "legacy data" {
			t.Fatal("legacy data changed", err)
		}
	}
}

func TestDataDirectoryPermissionDenied(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("requires Unix permission enforcement")
	}
	root := t.TempDir()
	if err := os.Chmod(root, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(root, 0700) })
	dir := filepath.Join(root, "data")
	if _, err := dataDirectory(dir); !errors.Is(err, os.ErrPermission) || !strings.Contains(err.Error(), dir) {
		t.Fatalf("permission error with the selected path: %v", err)
	}
}
