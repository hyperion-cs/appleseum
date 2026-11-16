package applekit

import (
	"cmp"
	"crypto/rand"
	_ "embed"
	"encoding/json"
	"fmt"
	"math/big"
	"slices"
	"strconv"
	"strings"

	"github.com/hyperion-cs/appleseum/applekit/internal/httpx"
)

type DeviceModel struct {
	Id    string `json:"id"`
	Name  string `json:"name"`
	Year  int    `json:"year"`
	MinOs int    `json:"minOS"`
	MaxOs int    `json:"maxOS"`
}
type MacOsVersion struct {
	Version string `json:"version"`
	Build   string `json:"build"`
}

//go:embed assets/device-models.json
var modelData []byte

//go:embed assets/macos-versions.json
var versionData []byte

var deviceModels = decodePlatformData[DeviceModel](modelData)
var macOsVersions = decodePlatformData[MacOsVersion](versionData)

func decodePlatformData[T any](data []byte) []T {
	var values []T
	if err := json.Unmarshal(data, &values); err != nil {
		panic(err)
	}
	return values
}

func DeviceModels() []DeviceModel {
	models := slices.Clone(deviceModels)
	slices.SortStableFunc(models, func(a, b DeviceModel) int { return cmp.Compare(b.Year, a.Year) })
	return models
}

func MacOsVersions() []MacOsVersion {
	versions := slices.Clone(macOsVersions)
	slices.SortFunc(versions, func(a, b MacOsVersion) int { return -slices.Compare(versionParts(a.Version), versionParts(b.Version)) })
	return versions
}

func initialDeviceSettings(settings DeviceSettings) (DeviceSettings, error) {
	if settings.Model != "" || settings.OsVersion != "" {
		return settings.withPlatformDefaults(), nil
	}
	var candidates []DeviceSettings
	major, branches := 0, 0
	// Use the newest release in each of the two newest catalog branches.
	for _, version := range MacOsVersions() {
		next := versionParts(version.Version)[0]
		if next == major {
			continue
		}
		major, branches = next, branches+1
		if branches > 2 {
			break
		}
		for _, model := range deviceModels {
			if major >= model.MinOs && major <= model.MaxOs {
				candidate := settings
				candidate.Model, candidate.OsVersion = model.Id, version.Version
				candidates = append(candidates, candidate)
			}
		}
	}
	if len(candidates) == 0 {
		return DeviceSettings{}, fmt.Errorf("no compatible initial device profiles")
	}
	index, err := rand.Int(rand.Reader, big.NewInt(int64(len(candidates))))
	if err != nil {
		return DeviceSettings{}, err
	}
	return candidates[index.Int64()], nil
}

func versionParts(version string) []int {
	parts := strings.Split(version, ".")
	values := make([]int, len(parts))
	for i, part := range parts {
		values[i], _ = strconv.Atoi(part)
	}
	return values
}

func (d DeviceSettings) withPlatformDefaults() DeviceSettings {
	p := httpx.Platform{Model: d.Model, Version: d.OsVersion}.Defaults()
	d.Model, d.OsVersion = p.Model, p.Version
	return d
}

func (d DeviceSettings) platform() (httpx.Platform, error) {
	d = d.withPlatformDefaults()
	model := slices.IndexFunc(deviceModels, func(m DeviceModel) bool { return m.Id == d.Model })
	version := slices.IndexFunc(macOsVersions, func(v MacOsVersion) bool { return v.Version == d.OsVersion })
	major := versionParts(d.OsVersion)[0]
	if model < 0 || version < 0 || major < deviceModels[model].MinOs || major > deviceModels[model].MaxOs {
		return httpx.Platform{}, fmt.Errorf("unsupported device model or macOS version")
	}
	return httpx.Platform{Model: d.Model, Version: d.OsVersion, Build: macOsVersions[version].Build}, nil
}

func (o AuthenticatorOptions) platform() httpx.Platform {
	p, _ := o.settings().platform() // Options are validated before use.
	return p
}
