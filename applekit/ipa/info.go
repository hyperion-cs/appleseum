// Package ipa reads app metadata and artwork from IPA archives.
package ipa

import (
	"archive/zip"
	"fmt"
	"io"
	"strings"

	"github.com/hyperion-cs/appleseum/applekit/artwork"
	"github.com/hyperion-cs/appleseum/applekit/internal/bundle"
	"howett.net/plist"
)

const (
	maxMetadataBytes = 1_048_576
)

// Metadata describes the main app bundle in an IPA.
type Metadata struct {
	Bundle string
	// Version is CFBundleVersion, used in installation manifests.
	Version string
	Name    string
	// DisplayVersion is CFBundleShortVersionString, falling back to Version.
	DisplayVersion string
	MinimumOs      string
}

// Inspect reads the main app metadata without contacting Apple.
func Inspect(path string) (Metadata, error) {
	archive, err := zip.OpenReader(path)
	if err != nil {
		return Metadata{}, err
	}
	defer archive.Close()
	for _, file := range archive.File {
		if bundle.MainName(file.Name) == "" {
			continue
		}
		if file.UncompressedSize64 > maxMetadataBytes {
			return Metadata{}, fmt.Errorf("app metadata is too large")
		}
		r, err := file.Open()
		if err != nil {
			return Metadata{}, err
		}
		body, err := io.ReadAll(io.LimitReader(r, maxMetadataBytes+1))
		r.Close()
		if err != nil {
			return Metadata{}, err
		}
		if len(body) > maxMetadataBytes {
			return Metadata{}, fmt.Errorf("app metadata is too large")
		}
		info, err := bundle.Decode(body)
		if err != nil {
			return Metadata{}, err
		}
		if info.Bundle == "" || info.Version == "" {
			return Metadata{}, fmt.Errorf("missing bundle identifier or version")
		}
		return Metadata{Bundle: info.Bundle, Version: info.Version, Name: info.Name, DisplayVersion: info.DisplayVersion, MinimumOs: info.MinimumOs}, nil
	}
	return Metadata{}, fmt.Errorf("app Info.plist not found")
}

// Artwork returns decodable artwork bytes from an IPA, without UI resizing.
func Artwork(path string) ([]byte, error) {
	archive, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	defer archive.Close()

	read := func(file *zip.File) ([]byte, error) {
		if file.UncompressedSize64 > artwork.MaxBytes {
			return nil, fmt.Errorf("icon too large")
		}
		reader, err := file.Open()
		if err != nil {
			return nil, err
		}
		defer reader.Close()
		return io.ReadAll(io.LimitReader(reader, artwork.MaxBytes+1))
	}

	for _, file := range archive.File {
		if file.Name == "iTunesArtwork" || file.Name == "iTunesArtwork@2x" {
			if raw, err := read(file); err == nil {
				if _, err := artwork.Decode(raw); err == nil {
					return raw, nil
				}
			}
		}
	}
	var prefix string
	var names []string
	for _, file := range archive.File {
		if bundle.MainName(file.Name) != "" {
			prefix = strings.TrimSuffix(file.Name, "Info.plist")
			var info struct {
				Files []string `plist:"CFBundleIconFiles"`
				File  string   `plist:"CFBundleIconFile"`
				Icons struct {
					Primary struct {
						Files []string `plist:"CFBundleIconFiles"`
					} `plist:"CFBundlePrimaryIcon"`
				} `plist:"CFBundleIcons"`
			}
			raw, err := read(file)
			if err == nil {
				plist.Unmarshal(raw, &info)
			}
			names = append(names, info.Icons.Primary.Files...)
			names = append(names, info.Files...)
			if info.File != "" {
				names = append(names, info.File)
			}
			break
		}
	}
	var best []byte
	bestSize := 0
	for _, file := range archive.File {
		if prefix == "" || !strings.HasPrefix(file.Name, prefix) {
			continue
		}
		base := strings.TrimPrefix(file.Name, prefix)
		if strings.Contains(base, "/") || !strings.HasSuffix(strings.ToLower(base), ".png") {
			continue
		}
		match := strings.HasPrefix(base, "AppIcon")
		for _, name := range names {
			name = strings.TrimSuffix(name, ".png")
			if base == name+".png" || strings.HasPrefix(base, name+"@") || strings.HasPrefix(base, name+"~") {
				match = true
			}
		}
		if !match {
			continue
		}
		raw, err := read(file)
		if err != nil {
			continue
		}
		icon, err := artwork.Decode(raw)
		if err == nil {
			size := icon.Bounds().Dx() * icon.Bounds().Dy()
			if size > bestSize {
				best, bestSize = raw, size
			}
		}
	}
	if best == nil {
		return nil, fmt.Errorf("app icon not found")
	}
	return best, nil
}
