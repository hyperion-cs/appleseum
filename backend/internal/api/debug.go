package api

import (
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"time"

	"appleseum/backend/internal/debuglog"
	"appleseum/backend/internal/fault"
	"appleseum/backend/internal/wire"
)

func (a *Server) journal(r *http.Request, _ visit) (wire.DebugPage, error) {
	filter, page, err := debuglog.ParseQuery(r.URL.Query())
	if err != nil {
		return wire.DebugPage{}, err
	}
	var result wire.DebugPage
	var total int
	result.Items, total, result.Next, result.Previous, err = debuglog.Page(a.debug.Events(), filter, page)
	result.Total = int64(total)
	return result, err
}

func (a *Server) process(*http.Request, visit) (wire.DebugInfo, error) { return a.debug.Process(), nil }

func (a *Server) clearJournal(*http.Request, visit) (none, error) { return none{}, a.debug.Clear() }

func (a *Server) selectExchanges(r *http.Request, _ visit) (wire.DebugSelectionResult, error) {
	query := r.URL.Query()
	for key, values := range query {
		if key != "source" && key != "q" || key == "q" && len(values) != 1 {
			return wire.DebugSelectionResult{}, fault.InvalidRequest
		}
	}
	filter, err := debuglog.NewFilter(query["source"], query.Get("q"))
	if err != nil {
		return wire.DebugSelectionResult{}, err
	}
	ids, err := a.debug.Selected(filter)
	return wire.DebugSelectionResult{Ids: ids}, err
}

func attachment(w http.ResponseWriter, contentType, name string, modified time.Time) {
	header := w.Header()
	header.Set("Cache-Control", "no-store")
	header.Set("Content-Type", contentType)
	header.Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, name))
	if !modified.IsZero() {
		header.Set("Last-Modified", modified.UTC().Format(http.TimeFormat))
	}
}

// downloadExchange sends one captured exchange as HAR, or as captured with raw=1.
func (a *Server) downloadExchange(w http.ResponseWriter, r *http.Request, _ visit) error {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		return fault.NotFound
	}
	exchanges, err := a.debug.OpenExchanges([]int64{id})
	if err != nil {
		return err
	}
	defer exchanges.Close()
	if r.URL.Query().Get("raw") == "1" {
		name := filepath.Base(exchanges.Raw().Name())
		attachment(w, "application/octet-stream", name, time.Time{})
		http.ServeContent(w, r, name, exchanges.Modified, exchanges.Raw())
		return nil
	}
	a.sendHar(w, r, exchanges, fmt.Sprintf("exchange-%d.har", id))
	return nil
}

func (a *Server) exportExchanges(w http.ResponseWriter, r *http.Request, _ visit) error {
	input, err := decode[wire.DebugExportRequest](w, r)
	if err == nil {
		err = input.Validate()
	}
	if err != nil {
		return err
	}
	exchanges, err := a.debug.OpenExchanges(input.Ids)
	if err != nil {
		return err
	}
	defer exchanges.Close()
	a.sendHar(w, r, exchanges, "exchanges.har")
	return nil
}

func (a *Server) sendHar(w http.ResponseWriter, r *http.Request, exchanges *debuglog.Exchanges, name string) {
	attachment(w, "application/json; charset=utf-8", name, exchanges.Modified)
	if r.Method == http.MethodHead {
		return
	}
	if err := exchanges.WriteHar(r.Context(), w); err != nil {
		a.debug.ExportError(w, r, "har_export", err)
		// The document is cut short; abort so the client does not take it for complete.
		panic(http.ErrAbortHandler)
	}
}

func (a *Server) downloadJournal(w http.ResponseWriter, _ *http.Request, _ visit) error {
	data, modified := a.debug.Csv()
	attachment(w, "application/octet-stream", "events.csv", modified)
	w.Write(data)
	return nil
}
