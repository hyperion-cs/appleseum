package api

import (
	"net/http"

	"appleseum/backend/internal/wire"

	"appleseum/backend/internal/fault"
	"appleseum/backend/internal/session"
)

const (
	// anyone: also a browser that has not signed in.
	anyone access = iota
	// reader: whoever may see the library — anyone locally, an account or the service publicly.
	reader
	// hiddenReader is reader, but a stranger learns nothing: the answer is not_found.
	hiddenReader
	// owner: a signed-in Apple account.
	owner
	// administrator: device settings; the service account has no device, so for it they do not exist.
	administrator
	// debugging: the journal, when the server runs with it.
	debugging
)

const (
	noKey keyUse = iota
	// keyOffered: sign-in creates the session the key will protect.
	keyOffered
	// keyRequired: the route calls Apple with the saved session.
	keyRequired
)

// access is what a route requires of whoever calls it.
type access int

func (a access) allows(actor session.Actor) error {
	switch {
	case a == reader && !actor.Library(), a == owner && !actor.SignedIn:
		return fault.SignInRequired
	case a == hiddenReader && !actor.Library(), a == administrator && actor.Service:
		return fault.NotFound
	}
	return nil
}

// keyUse is how a route relates to the browser's key of the saved Apple session.
type keyUse int

type route struct {
	pattern string
	class   RateClass
	access  access
	key     keyUse
	endpoint
}

func (a *Server) table() []route {
	routes := []route{
		{"GET /api/health", Read, anyone, noKey, out(http.StatusOK, a.health)},
		{"GET /api/state", Read, anyone, noKey, out(http.StatusOK, a.state)},
		{"PUT /api/preferences", Action, anyone, noKey, in(http.StatusOK, a.savePreferences)},
		{"PUT /api/table-settings/catalog", Action, anyone, noKey, in(http.StatusOK, a.saveCatalogView)},
		{"PUT /api/table-settings/library", Action, anyone, noKey, in(http.StatusOK, a.saveLibraryView)},
		{"PUT /api/table-settings/shares", Action, anyone, noKey, in(http.StatusOK, a.saveShareView)},
		{"POST /api/login", LoginPeer, anyone, keyOffered, raw(a.login, wire.LoginRequest{}, wire.Account{}, wire.ServiceLogin{})},
		{"POST /api/logout", Action, anyone, noKey, raw(a.logout, nil)},

		{"GET /api/profile", Read, owner, noKey, out(http.StatusOK, a.profile)},
		{"POST /api/profile/refresh", Action, owner, keyRequired, out(http.StatusOK, a.refreshProfile)},
		{"DELETE /api/profile", Action, owner, noKey, out(http.StatusNoContent, a.clearProfile)},

		{"GET /api/catalog", Read, owner, noKey, out(http.StatusOK, a.catalog)},
		{"POST /api/catalog/refresh", Action, owner, keyRequired, out(http.StatusAccepted, a.refreshCatalog)},
		{"DELETE /api/catalog", Action, owner, noKey, out(http.StatusNoContent, a.clearCatalog)},
		{"POST /api/catalog/{id}/versions", Action, owner, keyRequired, raw(a.versions, wire.VersionPageRequest{}, wire.VersionPage{}, wire.VersionLoadEvent{})},
		{"POST /api/catalog/selection", Read, owner, noKey, in(http.StatusOK, a.catalogSelection)},
		{"POST /api/downloads", Action, owner, keyRequired, in(http.StatusAccepted, a.queueDownloads)},
		{"POST /api/downloads/{id}/cancel", Action, reader, noKey, out(http.StatusAccepted, a.cancelDownload)},

		{"GET /api/library", Read, reader, noKey, out(http.StatusOK, a.libraryPage)},
		{"POST /api/library/selection", Read, reader, noKey, in(http.StatusOK, a.librarySelection)},
		{"PUT /api/library/imports/{id}", Action, reader, noKey, raw(a.importIpa, nil, wire.Created{})},
		{"POST /api/library/delete", Action, reader, noKey, in(http.StatusNoContent, a.deleteFiles)},
		{"POST /api/library/export", Action, reader, noKey, in(http.StatusCreated, a.prepareExport)},
		{"GET /api/library/exports/{id}", Files, hiddenReader, noKey, raw(a.downloadExport, nil)},
		{"GET /api/icons/{name}", Read, hiddenReader, noKey, raw(a.icon, nil)},

		{"GET /api/shares", Read, reader, noKey, out(http.StatusOK, a.sharesPage)},
		{"GET /api/shares/{id}/progress/{trace}", Read, reader, noKey, out(http.StatusOK, a.installationProgress)},
		{"GET /api/shares/{id}", Read, reader, noKey, out(http.StatusOK, a.share)},
		{"POST /api/shares", Action, reader, noKey, in(http.StatusCreated, a.startShare)},
		{"POST /api/shares/selection", Read, reader, noKey, in(http.StatusOK, a.selectShares)},
		{"POST /api/shares/stop", Action, reader, noKey, in(http.StatusNoContent, a.stopShares)},

		{"GET /api/debug", Debug, debugging, noKey, out(http.StatusOK, a.journal)},
		{"GET /api/debug/info", Debug, debugging, noKey, out(http.StatusOK, a.process)},
		{"DELETE /api/debug", Action, debugging, noKey, out(http.StatusNoContent, a.clearJournal)},
		{"GET /api/debug/selection", Debug, debugging, noKey, out(http.StatusOK, a.selectExchanges)},
		{"GET /api/debug/responses/{id}", Debug, debugging, noKey, raw(a.downloadExchange, nil)},
		{"POST /api/debug/responses", Debug, debugging, noKey, raw(a.exportExchanges, wire.DebugExportRequest{})},
		{"GET /api/debug/csv", Debug, debugging, noKey, raw(a.downloadJournal, nil)},
	}
	if a.cfg.Admin {
		routes = append(routes,
			route{"GET /api/admin/device", Read, administrator, noKey, out(http.StatusOK, a.device)},
			route{"PUT /api/admin/device", Action, administrator, noKey, in(http.StatusOK, a.saveDevice)},
			route{"POST /api/admin/device/defaults", Action, administrator, noKey, out(http.StatusOK, a.restoreDevice)},
			route{"POST /api/admin/device/reset", Action, administrator, noKey, out(http.StatusOK, a.resetDevice)},
		)
	}
	return routes
}
