package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"appleseum/backend/internal/product"
	"appleseum/backend/internal/rawlog"
)

// defaultDataDirectory selects a per-user directory without consulting the working directory.
func defaultDataDirectory(platform string, getenv func(string) string) (string, error) {
	var variable string
	var parts []string
	switch platform {
	case "windows":
		variable = "LOCALAPPDATA"
		parts = []string{product.DisplayName, "data"}
	case "darwin":
		variable = "HOME"
		parts = []string{"Library", "Application Support", product.DisplayName}
	default:
		if base := getenv("XDG_DATA_HOME"); filepath.IsAbs(base) {
			return filepath.Join(base, product.Name), nil
		}
		variable = "HOME"
		parts = []string{".local", "share", product.Name}
	}
	base := getenv(variable)
	if !filepath.IsAbs(base) {
		return "", fmt.Errorf("%s must contain an absolute path; specify --data to choose a directory", variable)
	}
	return filepath.Join(append([]string{base}, parts...)...), nil
}

func dataDirectory(data string) (string, error) {
	defer rawlog.Begin("startup data directory")()
	if data == "" {
		var err error
		data, err = defaultDataDirectory(runtime.GOOS, os.Getenv)
		if err != nil {
			return "", err
		}
	}
	dir, err := filepath.Abs(data)
	if err != nil {
		return "", fmt.Errorf("resolve data directory %q: %w", data, err)
	}
	if err = os.MkdirAll(filepath.Join(dir, "packages"), 0o700); err != nil {
		return "", fmt.Errorf("create data directory %q: %w", dir, err)
	}
	rawlog.Log("data directory=%q", dir)
	return dir, nil
}
