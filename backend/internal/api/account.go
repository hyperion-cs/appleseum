package api

import (
	"net/http"

	"appleseum/backend/internal/fault"
	"appleseum/backend/internal/session"
	"appleseum/backend/internal/wire"
)

func (a *Server) health(*http.Request, visit) (wire.Health, error) { return wire.Health{Ok: true}, nil }

func (a *Server) state(r *http.Request, v visit) (wire.State, error) {
	return v.space.State(r.Context())
}

func (a *Server) savePreferences(r *http.Request, v visit, input wire.PreferencesInput) (wire.Preferences, error) {
	return a.db.SavePreferences(r.Context(), v.space.Id, input)
}

// The sort key must be one its list offers to this requester.
func sortable(v visit, table wire.Table, sort string) error {
	if !v.actor.Scope().Sortable(table, sort) {
		return fault.InvalidRequest
	}
	return nil
}

func (a *Server) saveCatalogView(r *http.Request, v visit, view wire.CatalogView) (wire.Saved[wire.CatalogView], error) {
	if err := sortable(v, wire.CatalogTable, view.Sort); err != nil {
		return wire.Saved[wire.CatalogView]{}, err
	}
	// Apple reports no status for delisted apps, so that view cannot be sorted by it.
	if view.Delisted && view.Sort == "status" {
		view.Sort, view.Direction = "name", wire.Ascending
	}
	return a.db.SaveCatalogView(r.Context(), v.space.Id, view)
}

func (a *Server) saveLibraryView(r *http.Request, v visit, view wire.LibraryView) (wire.Saved[wire.LibraryView], error) {
	if err := sortable(v, wire.LibraryTable, view.Sort); err != nil {
		return wire.Saved[wire.LibraryView]{}, err
	}
	return a.db.SaveLibraryView(r.Context(), v.space.Id, view)
}

func (a *Server) saveShareView(r *http.Request, v visit, view wire.ShareView) (wire.Saved[wire.ShareView], error) {
	if err := sortable(v, wire.SharesTable, view.Sort); err != nil {
		return wire.Saved[wire.ShareView]{}, err
	}
	return a.db.SaveShareView(r.Context(), v.space.Id, view)
}

func (a *Server) login(w http.ResponseWriter, r *http.Request, v visit) error {
	input, err := decode[wire.LoginRequest](w, r)
	if err == nil {
		err = input.Validate()
	}
	if err != nil {
		return err
	}
	signedIn, err := v.space.Login(r.Context(), input)
	switch {
	case err != nil:
		return err
	case signedIn == nil:
		writeJson(w, http.StatusOK, wire.ServiceLogin{Service: true})
	default:
		writeJson(w, http.StatusOK, signedIn)
	}
	return nil
}

func (a *Server) logout(w http.ResponseWriter, r *http.Request, v visit) error {
	ended, err := v.space.Logout(r.Context())
	if ended {
		ClearSessionCookie(w)
	}
	if err == nil {
		w.WriteHeader(http.StatusNoContent)
	}
	return err
}

func (a *Server) profile(r *http.Request, v visit) (wire.ProfileSummary, error) {
	return v.space.Profile(r.Context(), false)
}

func (a *Server) refreshProfile(r *http.Request, v visit) (wire.ProfileSummary, error) {
	return v.space.Profile(r.Context(), true)
}

func (a *Server) clearProfile(r *http.Request, v visit) (none, error) {
	return none{}, v.space.ClearProfile(r.Context())
}

func (a *Server) device(r *http.Request, v visit) (wire.DeviceProfile, error) {
	return v.space.Device(r.Context())
}

func (a *Server) saveDevice(r *http.Request, v visit, input wire.DeviceSettings) (wire.DeviceProfile, error) {
	return v.space.ChangeDevice(r.Context(), session.DeviceSettings, input)
}

func (a *Server) restoreDevice(r *http.Request, v visit) (wire.DeviceProfile, error) {
	return v.space.ChangeDevice(r.Context(), session.DeviceDefaults, wire.DeviceSettings{})
}

func (a *Server) resetDevice(r *http.Request, v visit) (wire.DeviceProfile, error) {
	return v.space.ChangeDevice(r.Context(), session.DeviceReset, wire.DeviceSettings{})
}
