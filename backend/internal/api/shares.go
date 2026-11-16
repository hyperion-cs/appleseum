package api

import (
	"cmp"
	"context"
	"net/http"
	"time"

	"appleseum/backend/internal/fault"
	"appleseum/backend/internal/library"
	"appleseum/backend/internal/rawlog"
	"appleseum/backend/internal/sharing"
	"appleseum/backend/internal/wire"
)

// describe completes a share for the interface: its installation link and icon address.
func (a *Server) describe(r *http.Request, share *wire.Share) {
	share.Url = a.cfg.Install.InstallUrl(hostingRequest(r), sharing.ManifestPath(share.Id))
	share.Icon = library.IconUrl(share.Icon)
}

func (a *Server) startShare(r *http.Request, v visit, input wire.Selection) (wire.Share, error) {
	if err := input.For(wire.LibraryTable); err != nil {
		return wire.Share{}, err
	}
	if err := a.verifyManifestHost(r, v); err != nil {
		return wire.Share{}, err
	}
	share, err := sharing.Start(r.Context(), a.db, v.actor.Scope(), v.actor.ShareOwner(), v.space.Id, input)
	if err == nil {
		a.describe(r, &share)
	}
	return share, err
}

// verifyManifestHost checks, when the share is created on the device that
// will install it, that the device can resolve the manifest's host name.
func (a *Server) verifyManifestHost(r *http.Request, v visit) error {
	request := hostingRequest(r)
	if !a.cfg.Install.RequiresManifestHostCheck(request) {
		return nil
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	defer context.AfterFunc(v.space.Context(), cancel)()
	started := time.Now()
	err := a.cfg.Install.VerifyManifestHost(ctx, request, a.cfg.Lookup)
	event := wire.DebugEvent{Source: "Hosting", Method: "DNS", Target: "manifest_dns", Status: http.StatusOK, DurationMs: time.Since(started).Milliseconds(), Detail: "verified"}
	if err != nil {
		event.Status, event.Detail = http.StatusBadGateway, err.Error()
	}
	rawlog.Log("manifest DNS check duration_ms=%d status=%d detail=%s", event.DurationMs, event.Status, event.Detail)
	a.debug.Event(event)
	if err != nil {
		return fault.DeliveryVerificationFailed
	}
	return nil
}

func (a *Server) sharesPage(r *http.Request, v visit) (wire.SharePage, error) {
	query, err := listQuery(r.URL.Query(), wire.SharesTable)
	if err != nil {
		return wire.SharePage{}, err
	}
	page, err := a.db.Shares(r.Context(), v.actor.ShareScope(), query)
	for i := range page.Items {
		a.describe(r, &page.Items[i])
	}
	return page, err
}

func (a *Server) share(r *http.Request, v visit) (wire.Share, error) {
	id := r.PathValue("id")
	if !wire.Uuid(id) {
		return wire.Share{}, fault.NotFound
	}
	share, err := a.db.Share(r.Context(), v.actor.ShareScope(), id)
	if err == nil {
		a.describe(r, &share)
	}
	return share, err
}

// selectShares resolves "select all" of the share list.
func (a *Server) selectShares(r *http.Request, v visit, input wire.Selection) (wire.SelectionResult, error) {
	if err := input.For(wire.SharesTable); err != nil || input.Filter == nil {
		return wire.SelectionResult{}, cmp.Or(err, error(fault.InvalidRequest))
	}
	ids, err := a.db.SelectShares(r.Context(), v.actor.ShareScope(), input.Filter.Query)
	return wire.SelectionResult{Ids: ids}, err
}

func (a *Server) stopShares(r *http.Request, v visit, input wire.Selection) (none, error) {
	if err := input.For(wire.SharesTable); err != nil {
		return none{}, err
	}
	return none{}, a.db.StopShares(r.Context(), v.actor.ShareScope(), input)
}

func (a *Server) installationProgress(r *http.Request, v visit) (wire.InstallationProgress, error) {
	id, trace := r.PathValue("id"), r.PathValue("trace")
	if !wire.Uuid(id) || !sharing.ValidTrace(trace) {
		return wire.InstallationProgress{}, fault.NotFound
	}
	if _, err := a.db.Share(r.Context(), v.actor.ShareScope(), id); err != nil {
		return wire.InstallationProgress{}, err
	}
	return a.progress.Snapshot(id, trace), nil
}
