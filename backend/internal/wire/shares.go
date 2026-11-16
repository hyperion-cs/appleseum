package wire

type Share struct {
	Cursor    string `json:"cursor"`
	Owner     string `json:"owner"`
	CreatedAt string `json:"createdAt"`
	Name      string `json:"name"`
	Icon      string `json:"icon"`
	Id        string `json:"id"`
	Url       string `json:"url"`
	Count     int    `json:"count"`
}

type SharePage = Page[Share]

// InstallationProgress reports delivery observed by the server, not iOS installation.
// Percent weighs every package of the share by its size.
type InstallationProgress struct {
	Stage   InstallationStage `json:"stage"`
	Percent int               `json:"percent"`
	// Skipped counts the share's apps the device never requested once delivery
	// is complete; iOS does not download an app that is already installed.
	Skipped int `json:"skipped"`
}
