package wire

import "appleseum/backend/internal/fault"

// Page is one keyset page of a list; Total counts every matching row.
type Page[T any] struct {
	Total    int64 `json:"total"`
	Items    []T   `json:"items"`
	Next     bool  `json:"next"`
	Previous bool  `json:"previous"`
}

// Filter matches every row a list shows with these settings. Delisted
// applies to the catalog, Mine to the library; shares match by text only.
type Filter struct {
	Query    string `json:"query"`
	Delisted bool   `json:"delisted"`
	Mine     bool   `json:"mine"`
}

// Selection names rows by id or by a filter, never both.
type Selection struct {
	Ids    []string `json:"ids"`
	Filter *Filter  `json:"filter"`
}

type LibraryItem struct {
	Cursor    string         `json:"cursor"`
	CanManage bool           `json:"canManage"`
	Icon      string         `json:"icon"`
	Status    DownloadStatus `json:"status"`
	Received  int64          `json:"received"`
	Total     int64          `json:"total"`
	Speed     float64        `json:"speed"`
	Error     fault.Code     `json:"error"`
	Id        string         `json:"id"`
	AppId     string         `json:"appId"`
	Name      string         `json:"name"`
	Version   string         `json:"version"`
	MinimumOs string         `json:"minimumOs"`
	Size      int64          `json:"size"`
	CreatedAt string         `json:"createdAt"`
}

type LibraryPage = Page[LibraryItem]

type FileExport struct {
	Url string `json:"url"`
}

type SelectionResult struct {
	Ids []string `json:"ids"`
}

// LibrarySelectionResult also says which selected files are ready and which the requester may change.
type LibrarySelectionResult struct {
	Ids       []string `json:"ids"`
	ReadyIds  []string `json:"readyIds"`
	ManageIds []string `json:"manageIds"`
}
