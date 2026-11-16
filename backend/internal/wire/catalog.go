package wire

import "appleseum/backend/internal/fault"

type CatalogItem struct {
	Cursor      string `json:"cursor"`
	PurchasedAt string `json:"purchasedAt"`
	Icon        string `json:"icon"`
	Size        *int64 `json:"size"`
	InLibrary   bool   `json:"inLibrary"`
	Id          string `json:"id"`
	Name        string `json:"name"`
	Delisted    bool   `json:"delisted"`
}

type CatalogPage = Page[CatalogItem]

// DownloadRequest queues the selected apps; Versions picks other versions than the latest.
type DownloadRequest struct {
	Selection Selection         `json:"selection"`
	Versions  []DownloadVersion `json:"versions"`
}

type DownloadVersion struct {
	AppId     string `json:"appId"`
	VersionId string `json:"versionId"`
}

type VersionPageRequest struct {
	Move     string `json:"move"`
	Revision string `json:"revision"`
	Cursor   string `json:"cursor"`
	Limit    int    `json:"limit"`
}

type VersionLoadEvent struct {
	Progress int          `json:"progress"`
	Page     *VersionPage `json:"page"`
	Error    fault.Code   `json:"error"`
}

type AppVersion struct {
	Cursor    string     `json:"cursor"`
	Id        string     `json:"id"`
	Version   string     `json:"version"`
	MinimumOs string     `json:"minimumOs"`
	Latest    bool       `json:"latest"`
	Error     fault.Code `json:"error"`
}

type VersionPage struct {
	Revision string       `json:"revision"`
	Name     string       `json:"name"`
	Total    int          `json:"total"`
	Items    []AppVersion `json:"items"`
	Next     bool         `json:"next"`
	Previous bool         `json:"previous"`
}
