package api

import (
	"archive/zip"
	"cmp"
	"io"
	"mime"
	"net/http"
	"os"
	"strconv"

	"appleseum/backend/internal/artwork"
	"appleseum/backend/internal/fault"
	"appleseum/backend/internal/library"
	"appleseum/backend/internal/wire"
)

const (
	exportArchiveName = "appleseum-apps.zip"
	yearSeconds       = "31536000"
)

func (a *Server) catalog(r *http.Request, v visit) (wire.CatalogPage, error) {
	query, err := listQuery(r.URL.Query(), wire.CatalogTable)
	if err != nil {
		return wire.CatalogPage{}, err
	}
	page, err := a.db.Catalog(r.Context(), v.actor.Scope(), query)
	for i := range page.Items {
		page.Items[i].Icon = library.IconUrl(page.Items[i].Icon)
	}
	return page, err
}

func (a *Server) refreshCatalog(_ *http.Request, v visit) (none, error) {
	return none{}, v.space.RefreshCatalog()
}

func (a *Server) clearCatalog(r *http.Request, v visit) (none, error) {
	return none{}, v.space.ClearCatalog(r.Context())
}

func (a *Server) libraryPage(r *http.Request, v visit) (wire.LibraryPage, error) {
	query, err := listQuery(r.URL.Query(), wire.LibraryTable)
	if err != nil {
		return wire.LibraryPage{}, err
	}
	page, err := a.db.Library(r.Context(), v.actor.Scope(), query)
	for i := range page.Items {
		page.Items[i].Icon = library.IconUrl(page.Items[i].Icon)
	}
	return page, err
}

// catalogSelection resolves "select all" and re-checks selected apps: only
// those that can still be downloaded remain.
func (a *Server) catalogSelection(r *http.Request, v visit, input wire.Selection) (wire.SelectionResult, error) {
	if err := input.For(wire.CatalogTable); err != nil {
		return wire.SelectionResult{}, err
	}
	apps, err := a.db.CatalogSelection(r.Context(), v.actor.Scope(), input, true)
	result := wire.SelectionResult{Ids: make([]string, len(apps))}
	for i, app := range apps {
		result.Ids[i] = strconv.FormatInt(app, 10)
	}
	return result, err
}

// librarySelection resolves "select all" and re-checks selected files: which
// are ready and which the requester may change.
func (a *Server) librarySelection(r *http.Request, v visit, input wire.Selection) (wire.LibrarySelectionResult, error) {
	if err := input.For(wire.LibraryTable); err != nil {
		return wire.LibrarySelectionResult{}, err
	}
	scope := v.actor.Scope()
	entries, err := a.db.LibrarySelection(r.Context(), scope, input, true)
	if fault.Is(err, fault.NotFound) {
		// A selected file that is gone invalidates the selection, not the request.
		err = fault.InvalidSelection
	}
	result := wire.LibrarySelectionResult{Ids: []string{}, ReadyIds: []string{}, ManageIds: []string{}}
	for _, entry := range entries {
		result.Ids = append(result.Ids, entry.Id)
		if entry.Status == wire.Done {
			result.ReadyIds = append(result.ReadyIds, entry.Id)
		}
		if scope.Owns(entry.Owner) {
			result.ManageIds = append(result.ManageIds, entry.Id)
		}
	}
	return result, err
}

func (a *Server) queueDownloads(r *http.Request, v visit, input wire.DownloadRequest) (none, error) {
	return none{}, v.space.Queue(r.Context(), input)
}

func (a *Server) cancelDownload(r *http.Request, v visit) (none, error) {
	id := r.PathValue("id")
	if !wire.Uuid(id) {
		return none{}, fault.NotFound
	}
	return none{}, a.library.Cancel(r.Context(), v.actor.Scope(), id)
}

func (a *Server) deleteFiles(r *http.Request, v visit, input wire.Selection) (none, error) {
	if err := input.For(wire.LibraryTable); err != nil {
		return none{}, err
	}
	return none{}, a.library.Delete(r.Context(), v.actor.Scope(), input)
}

func (a *Server) prepareExport(r *http.Request, v visit, input wire.Selection) (wire.FileExport, error) {
	if err := input.For(wire.LibraryTable); err != nil {
		return wire.FileExport{}, err
	}
	id, err := a.library.Prepare(r.Context(), &v.space.Exports, v.actor.Scope(), input)
	return wire.FileExport{Url: "/api/library/exports/" + id}, err
}

// downloadExport sends one IPA as it is, several as an uncompressed archive.
func (a *Server) downloadExport(w http.ResponseWriter, r *http.Request, v visit) error {
	files, release, err := a.library.Open(r.Context(), &v.space.Exports, v.actor.Scope(), r.PathValue("id"))
	if err != nil {
		return err
	}
	defer release()
	w.Header().Set("Cache-Control", "no-store")
	attachment := func(name string) string {
		return mime.FormatMediaType("attachment", map[string]string{"filename": name})
	}
	if len(files) == 1 {
		file, err := os.Open(a.db.PackagePath(files[0].Id))
		if err != nil {
			return fault.MissingPackage
		}
		defer file.Close()
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", attachment(files[0].Name))
		http.ServeContent(w, r, files[0].Name, files[0].Info.ModTime(), file)
		return nil
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", attachment(exportArchiveName))
	if r.Method == http.MethodHead {
		return nil
	}
	archive := zip.NewWriter(w)
	for _, item := range files {
		// The response has started, so a failure can only end it early.
		if err := addToArchive(archive, a.db.PackagePath(item.Id), item); err != nil || r.Context().Err() != nil {
			a.debug.ExportError(w, r, "file_export", err)
			return nil
		}
	}
	if err := archive.Close(); err != nil {
		a.debug.ExportError(w, r, "file_export", err)
	}
	return nil
}

func addToArchive(archive *zip.Writer, path string, item library.ExportFile) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	header := &zip.FileHeader{Name: item.Name, Method: zip.Store}
	header.SetModTime(item.Info.ModTime())
	entry, err := archive.CreateHeader(header)
	if err == nil {
		_, err = io.Copy(entry, file)
	}
	return err
}

func (a *Server) icon(w http.ResponseWriter, r *http.Request, v visit) error {
	name := r.PathValue("name")
	if !artwork.ValidName(name) {
		return fault.NotFound
	}
	if visible, err := a.db.CanReadIcon(r.Context(), v.actor.Scope(), name); err != nil || !visible {
		return cmp.Or(err, error(fault.NotFound))
	}
	file, err := os.Open(a.db.IconPath(name))
	if err != nil {
		return fault.NotFound
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "private, max-age="+yearSeconds+", immutable")
	http.ServeContent(w, r, name, stat.ModTime(), file)
	return nil
}
