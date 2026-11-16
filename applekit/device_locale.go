package applekit

import (
	"errors"
	"strings"

	"golang.org/x/text/language"
)

const (
	maxLocaleLength = 100
)

// WithLocale derives Apple's language, locale and compliance region from a
// BCP 47 tag with an explicit country, and validates the resulting settings.
// It returns a copy without changing the receiver or any stored device.
func (d DeviceSettings) WithLocale(locale string) (DeviceSettings, error) {
	if len(locale) > maxLocaleLength || strings.Contains(locale, "_") {
		return DeviceSettings{}, errors.New("invalid device locale")
	}
	tag, err := language.Parse(locale)
	base, _, region := tag.Raw()
	if err != nil || base.String() == "und" || len(region.String()) != 2 || region.String() == "ZZ" ||
		len(tag.Variants()) != 0 || len(tag.Extensions()) != 0 {
		return DeviceSettings{}, errors.New("invalid device locale")
	}
	d.Language, d.Locale = strings.ToLower(tag.String()), strings.ReplaceAll(tag.String(), "-", "_")
	d.ComplianceId = region.String()
	if err := d.validate(); err != nil {
		return DeviceSettings{}, err
	}
	return d, nil
}

// LocaleTag combines the saved language and compliance region as a BCP 47 tag.
// Reading it does not rewrite stored metadata whose language and region differ.
func (d DeviceSettings) LocaleTag() string {
	tag, err := language.Parse(d.Language)
	if err != nil {
		tag = language.Make(strings.ReplaceAll(d.Locale, "_", "-"))
	}
	region, err := language.ParseRegion(d.ComplianceId)
	if err == nil {
		tag, _ = language.Compose(tag, region)
	}
	return tag.String()
}
