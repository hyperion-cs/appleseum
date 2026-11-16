package applekit

import (
	"strings"
	"testing"
)

func TestDeviceLocaleDerivesAppleMetadata(t *testing.T) {
	initial := DeviceSettings{Model: "MacBookPro18,1", OsVersion: "26.5", TimeZone: "UTC"}
	for _, tc := range []struct{ input, tag, language, locale, region string }{
		{"en-gb", "en-GB", "en-gb", "en_GB", "GB"},
		{"sr-Latn-RS", "sr-Latn-RS", "sr-latn-rs", "sr_Latn_RS", "RS"},
		{"kk-KZ", "kk-KZ", "kk-kz", "kk_KZ", "KZ"},
	} {
		t.Run(tc.input, func(t *testing.T) {
			settings, err := initial.WithLocale(tc.input)
			if err != nil {
				t.Fatal(err)
			}
			want := initial
			want.Language, want.Locale, want.ComplianceId = tc.language, tc.locale, tc.region
			if settings != want || settings.LocaleTag() != tc.tag {
				t.Fatalf("settings=%+v tag=%q", settings, settings.LocaleTag())
			}
		})
	}
}

func TestDeviceLocaleRejectsAmbiguousOrInvalidSettings(t *testing.T) {
	initial := DefaultDeviceSettings()
	for _, locale := range []string{"", "en", "und-US", "ru_RU", "en-001", "en-ZZ", "en-US-u-ca-gregory", "de-DE-1901", "xx-US", "en-US\r\nInjected: value", strings.Repeat("a", 101)} {
		if _, err := initial.WithLocale(locale); err == nil {
			t.Errorf("accepted locale %q", locale)
		}
	}
	for _, invalid := range []DeviceSettings{
		{Model: "unknown", OsVersion: "26.5", TimeZone: "UTC"},
		{Model: "MacBookPro18,1", OsVersion: "99.9", TimeZone: "UTC"},
		{Model: "MacBookPro18,1", OsVersion: "26.5", TimeZone: "Missing/Zone"},
	} {
		if _, err := invalid.WithLocale("en-US"); err == nil {
			t.Errorf("accepted settings %+v", invalid)
		}
	}
}

func TestLocaleTagPreservesIndependentLanguageAndRegion(t *testing.T) {
	settings := DeviceSettings{Language: "ru-ru", Locale: "ru_RU", ComplianceId: "KZ"}
	before := settings
	if got := settings.LocaleTag(); got != "ru-KZ" || settings != before {
		t.Fatalf("tag=%q settings=%+v", got, settings)
	}
	settings.Language = "invalid_@"
	if got := settings.LocaleTag(); got != "ru-KZ" {
		t.Fatalf("locale fallback=%q", got)
	}
}
