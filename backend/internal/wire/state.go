package wire

import "appleseum/backend/internal/fault"

type PreferencesInput struct {
	Language Language `json:"language"`
	Theme    Theme    `json:"theme"`
}

type Preferences struct {
	Language Language `json:"language"`
	Theme    Theme    `json:"theme"`
	Revision int64    `json:"revision"`
}

// Each table saves its own order and filter; Revision orders saves made from several tabs.
type Saved[T any] struct {
	Value    T     `json:"value"`
	Revision int64 `json:"revision"`
}

type CatalogView struct {
	Sort      string    `json:"sort"`
	Direction Direction `json:"direction"`
	Delisted  bool      `json:"delisted"`
}

type LibraryView struct {
	Sort      string    `json:"sort"`
	Direction Direction `json:"direction"`
	Mine      bool      `json:"mine"`
}

type ShareView struct {
	Sort      string    `json:"sort"`
	Direction Direction `json:"direction"`
}

type Tables struct {
	Catalog Saved[CatalogView] `json:"catalog"`
	Library Saved[LibraryView] `json:"library"`
	Shares  Saved[ShareView]   `json:"shares"`
}

// Mode is how the server runs; it does not change while it runs.
type Mode struct {
	Public        bool `json:"public"`
	SharedLibrary bool `json:"sharedLibrary"`
	Service       bool `json:"service"`
	Admin         bool `json:"admin"`
	Debug         bool `json:"debug"`
	Ish           bool `json:"ish"`
}

// Auth is what signing in needs from the user. Busy is a sign-in in progress.
type Auth struct {
	State AuthState  `json:"state"`
	Busy  bool       `json:"busy"`
	Error fault.Code `json:"error"`
}

type Catalog struct {
	Status   CatalogStatus `json:"status"`
	Progress float64       `json:"progress"`
	Error    fault.Code    `json:"error"`
	Cached   bool          `json:"cached"`
}

type State struct {
	Csrf            string      `json:"csrf"`
	Mode            Mode        `json:"mode"`
	Account         *Account    `json:"account"`
	Auth            Auth        `json:"auth"`
	Catalog         Catalog     `json:"catalog"`
	StorageError    fault.Code  `json:"storageError"`
	DownloadsActive bool        `json:"downloadsActive"`
	ShareCount      int64       `json:"shareCount"`
	Tables          Tables      `json:"tables"`
	Preferences     Preferences `json:"preferences"`
}

type Error struct {
	Code fault.Code `json:"code"`
}

type Health struct {
	Ok bool `json:"ok"`
}

// ServiceLogin answers a sign-in to the service account, which has no Apple account.
type ServiceLogin struct {
	Service bool `json:"service"`
}

// Created names a new resource.
type Created struct {
	Id string `json:"id"`
}
