// Package bundle decodes main-app Info.plist metadata.
package bundle

import (
	"cmp"
	"strings"

	"github.com/hyperion-cs/appleseum/applekit/internal/plistutil"
)

// MainName excludes nested apps and extensions.
func MainName(path string) string {
	path, ok := strings.CutPrefix(path, "Payload/")
	if !ok {
		return ""
	}
	name, ok := strings.CutSuffix(path, ".app/Info.plist")
	if !ok || strings.Contains(name, "/") {
		return ""
	}
	return name
}

type Info struct {
	Bundle         string `plist:"CFBundleIdentifier"`
	Version        string `plist:"CFBundleVersion"`
	DisplayVersion string `plist:"CFBundleShortVersionString"`
	DisplayName    string `plist:"CFBundleDisplayName"`
	Name           string `plist:"CFBundleName"`
	Executable     string `plist:"CFBundleExecutable"`
	MinimumOs      string `plist:"MinimumOSVersion"`
}

func Decode(data []byte) (Info, error) {
	var info Info
	_, err := plistutil.Unmarshal(data, &info)
	info.Name = cmp.Or(info.DisplayName, info.Name)
	info.DisplayVersion = cmp.Or(info.DisplayVersion, info.Version)
	return info, err
}
