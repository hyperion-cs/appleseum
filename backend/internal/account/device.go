package account

import (
	"github.com/hyperion-cs/appleseum/applekit"

	"appleseum/backend/internal/wire"
)

func AppleSettings(d wire.DeviceSettings) (applekit.DeviceSettings, error) {
	return (applekit.DeviceSettings{Model: d.Model, OsVersion: d.OsVersion, TimeZone: d.TimeZone}).WithLocale(d.Locale)
}

func DeviceSettings(d applekit.DeviceSettings) wire.DeviceSettings {
	return wire.DeviceSettings{Locale: d.LocaleTag(), TimeZone: d.TimeZone, Model: d.Model, OsVersion: d.OsVersion}
}

func DeviceProfile(p applekit.DeviceProfile) wire.DeviceProfile {
	models, versions := applekit.DeviceModels(), applekit.MacOsVersions()
	profile := wire.DeviceProfile{
		Models: make([]wire.DeviceModel, len(models)), OsVersions: make([]wire.MacOsVersion, len(versions)),
		Settings: DeviceSettings(p.Settings), Defaults: DeviceSettings(p.Defaults),
		Id: p.Id, Guid: p.Guid, ClientVersion: p.ClientVersion,
	}
	for i, m := range models {
		profile.Models[i] = wire.DeviceModel{Id: m.Id, Name: m.Name, MinOs: m.MinOs, MaxOs: m.MaxOs}
	}
	for i, v := range versions {
		profile.OsVersions[i] = wire.MacOsVersion{Version: v.Version}
	}
	return profile
}
