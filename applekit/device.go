package applekit

import (
	"errors"
	"strings"
	"time"

	"github.com/hyperion-cs/appleseum/applekit/internal/adi"
	"github.com/hyperion-cs/appleseum/applekit/internal/ams"
	"github.com/hyperion-cs/appleseum/applekit/internal/httpx"
)

const (
	DefaultLanguage     = "en-us"
	DefaultLocale       = "en_US"
	DefaultTimeZone     = "UTC"
	DefaultComplianceId = "US"
	DefaultStorefront   = "143441"
)

const (
	uppercaseLatinLetters   = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	maxDeviceMetadataLength = 100
)

// DeviceSettings controls local metadata, independently of the account's store country.
type DeviceSettings struct {
	Model        string `json:"model,omitempty"`
	OsVersion    string `json:"osVersion,omitempty"`
	Language     string `json:"language"`
	Locale       string `json:"locale"`
	TimeZone     string `json:"timeZone"`
	ComplianceId string `json:"complianceId"`
}

type DeviceProfile struct {
	Settings        DeviceSettings `json:"settings"`
	Defaults        DeviceSettings `json:"defaults"`
	Id              string         `json:"id"`
	Guid            string         `json:"guid"`
	Model           string         `json:"model"`
	OsVersion       string         `json:"osVersion"`
	ClientVersion   string         `json:"clientVersion"`
	IdmsProvisioned bool           `json:"idmsProvisioned"`
	AmdProvisioned  bool           `json:"amdProvisioned"`
}

func DefaultDeviceSettings() DeviceSettings {
	return DeviceSettings{Language: DefaultLanguage, Locale: DefaultLocale, TimeZone: DefaultTimeZone, ComplianceId: DefaultComplianceId}.withPlatformDefaults()
}

// ValidDeviceSettings validates metadata without changing the installation.
func ValidDeviceSettings(d DeviceSettings) bool { return d.validate() == nil }

func (d DeviceSettings) validate() error {
	if _, err := d.platform(); err != nil {
		return err
	}
	if err := (ams.RequestContext{Actor: ams.Configurator, Storefront: DefaultStorefront, Language: d.Language}).Validate(); err != nil {
		return err
	}
	for _, v := range []string{d.Locale, d.TimeZone} {
		if v == "" || len(v) > maxDeviceMetadataLength || strings.ContainsFunc(v, func(r rune) bool { return r < ' ' || r > '~' }) {
			return errors.New("invalid Apple device locale")
		}
	}
	if len(d.ComplianceId) != 2 || strings.Trim(d.ComplianceId, uppercaseLatinLetters) != "" {
		return errors.New("invalid Apple device compliance region")
	}
	_, err := time.LoadLocation(d.TimeZone)
	return err
}

func (o AuthenticatorOptions) settings() DeviceSettings {
	return DeviceSettings{Model: o.Model, OsVersion: o.OsVersion, Language: o.Language, Locale: o.Locale, TimeZone: o.TimeZone, ComplianceId: o.ComplianceId}
}

func (o *AuthenticatorOptions) applySettings(d DeviceSettings) {
	d = d.withPlatformDefaults()
	o.Model, o.OsVersion = d.Model, d.OsVersion
	o.Language, o.Locale, o.TimeZone, o.ComplianceId = d.Language, d.Locale, d.TimeZone, d.ComplianceId
}

func (a *Authenticator) loadState() error {
	if a.state != nil {
		return nil
	}
	settings := a.options.settings()
	state, err := loadInstallation(a.options.Store, settings)
	if err != nil {
		return err
	}
	a.state = state
	a.options.applySettings(*state.Settings)
	return nil
}

func utcOffset(timeZone string, now time.Time) int {
	location, err := time.LoadLocation(timeZone)
	if err != nil {
		location = time.UTC
	}
	_, offset := now.In(location).Zone()
	return offset
}

func (o AuthenticatorOptions) requestContext(storefront string, now time.Time) ams.RequestContext {
	return ams.RequestContext{Actor: ams.Configurator, Storefront: storefront, Language: o.Language, UtcOffsetSeconds: utcOffset(o.TimeZone, now), Platform: o.platform()}
}

func (a *Authenticator) profile() DeviceProfile {
	s := a.state
	return DeviceProfile{
		Settings: a.options.settings(), Defaults: DefaultDeviceSettings(),
		Id: s.deviceId(), Guid: s.guid(),
		Model: a.options.Model, OsVersion: a.options.OsVersion + " / " + a.options.platform().Build,
		ClientVersion:   "Configurator " + httpx.ConfiguratorVersion,
		IdmsProvisioned: len(s.Idms) > 0 && s.IdmsGeneration == idmsProvisioningGeneration,
		AmdProvisioned:  len(s.Amd) > adi.AmdStateHeaderSize,
	}
}

func (a *Authenticator) Device() (DeviceProfile, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.loadState(); err != nil {
		return DeviceProfile{}, err
	}
	return a.profile(), nil
}

// SetDeviceSettings replaces the identity when the model changes.
// The caller owns invalidation of sessions using the previous identity.
func (a *Authenticator) SetDeviceSettings(d DeviceSettings) (DeviceProfile, error) {
	if err := d.validate(); err != nil {
		return DeviceProfile{}, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.loadState(); err != nil {
		return DeviceProfile{}, err
	}
	// Preserve saved platform fields when omitted.
	if d.Model == "" {
		d.Model = a.options.Model
	}
	if d.OsVersion == "" {
		d.OsVersion = a.options.OsVersion
	}
	if err := d.validate(); err != nil {
		return DeviceProfile{}, err
	}
	return a.storeDeviceSettings(d, d.Model != a.options.Model)
}

// ResetDevice replaces the identity and discards IdMS/AMD.
// The caller must invalidate clients using the previous identity.
func (a *Authenticator) ResetDevice() (DeviceProfile, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.loadState(); err != nil {
		return DeviceProfile{}, err
	}
	return a.storeDeviceSettings(a.options.settings(), true)
}

// Called with a.mu held; publish memory only after the atomic save succeeds.
func (a *Authenticator) storeDeviceSettings(settings DeviceSettings, replaceIdentity bool) (DeviceProfile, error) {
	next := *a.state
	if replaceIdentity {
		fresh, err := newInstallation()
		if err != nil {
			return DeviceProfile{}, err
		}
		next = *fresh
	}
	next.Settings = &settings
	if err := saveInstallation(a.options.Store, &next); err != nil {
		return DeviceProfile{}, err
	}
	a.state = &next
	a.options.applySettings(settings)
	if replaceIdentity {
		a.amd = nil
	}
	a.pending = nil
	return a.profile(), nil
}

// SetOsVersion preserves identity and authentication. Serialize with other client operations.
func (c *Client) SetOsVersion(version string) error {
	settings := DefaultDeviceSettings()
	settings.Model, settings.OsVersion = c.s.Platform().Model, version
	platform, err := settings.platform()
	if err != nil {
		return err
	}
	return c.s.SetPlatform(platform)
}

func (c *Client) SetDeviceSettings(settings DeviceSettings) error {
	if err := settings.validate(); err != nil {
		return err
	}
	platform, _ := settings.platform()
	return c.s.SetMetadata(platform, settings.Language, utcOffset(settings.TimeZone, time.Now()))
}
