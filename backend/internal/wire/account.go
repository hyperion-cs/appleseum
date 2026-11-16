package wire

import "appleseum/backend/internal/fault"

type Account struct {
	Email      string `json:"email"`
	Name       string `json:"name"`
	Id         string `json:"id"`
	Country    string `json:"country"`
	Storefront string `json:"storefront"`
	// SignedInAt is when Apple last accepted the password, or "" when unknown.
	SignedInAt string `json:"signedInAt"`
}

type LoginRequest struct {
	Remember bool   `json:"remember"`
	Email    string `json:"email"`
	Password string `json:"password"`
	Code     string `json:"code"`
}

// DeviceSettings: the interface picks one locale; Apple receives its language, locale and region.
type DeviceSettings struct {
	Model     string `json:"model"`
	OsVersion string `json:"osVersion"`
	Locale    string `json:"locale"`
	TimeZone  string `json:"timeZone"`
}

// DeviceProfile is the settings screen: the device's identifiers, its settings and what they may be.
type DeviceProfile struct {
	ReadOnly      bool           `json:"readOnly"`
	Models        []DeviceModel  `json:"models"`
	OsVersions    []MacOsVersion `json:"osVersions"`
	Settings      DeviceSettings `json:"settings"`
	Defaults      DeviceSettings `json:"defaults"`
	Id            string         `json:"id"`
	Guid          string         `json:"guid"`
	ClientVersion string         `json:"clientVersion"`
}

// DeviceModel is a Mac model and the major macOS versions it runs.
type DeviceModel struct {
	Id    string `json:"id"`
	Name  string `json:"name"`
	MinOs int    `json:"minOs"`
	MaxOs int    `json:"maxOs"`
}

type MacOsVersion struct {
	Version string `json:"version"`
}

// AppleProfile is what the profile screen shows of the account's Apple profile.
type AppleProfile struct {
	Nickname      string `json:"nickname"`
	FamilyRole    string `json:"familyRole"`
	Balance       string `json:"balance"`
	Subscriptions *int64 `json:"subscriptions"`
}

type ProfileSummary struct {
	CatalogApps  int64         `json:"catalogApps"`
	DelistedApps int64         `json:"delistedApps"`
	SignedInAt   string        `json:"signedInAt"`
	Apple        *AppleProfile `json:"apple"`
	AppleError   fault.Code    `json:"appleError"`
}
