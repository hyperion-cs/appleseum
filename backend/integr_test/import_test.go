package integration_test

import (
	"archive/zip"
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"howett.net/plist"

	"appleseum/backend/internal/db"
	"appleseum/backend/internal/wire"
)

func importFixture(t *testing.T, bundle, name string, padded ...bool) []byte {
	t.Helper()
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	if len(padded) > 0 && padded[0] {
		z.SetComment(strings.Repeat("p", 60_000))
	}
	info, err := z.Create("Payload/Test.app/Info.plist")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := plist.Marshal(map[string]string{"CFBundleIdentifier": bundle, "CFBundleVersion": "2", "CFBundleShortVersionString": "2.3", "MinimumOSVersion": "17.5", "CFBundleDisplayName": name}, plist.BinaryFormat)
	if err != nil {
		t.Fatal(err)
	}
	info.Write(raw)
	icon, _ := z.Create("iTunesArtwork")
	icon.Write(testIcon())
	// License and store metadata may identify someone other than the uploader.
	license, _ := z.Create("Payload/Test.app/SC_Info/Test.sinf")
	license.Write([]byte("unrelated-account-license-fixture"))
	metadata, _ := z.Create("iTunesMetadata.plist")
	metadata.Write([]byte(`<plist><dict><key>appleId</key><string>unrelated@example.test</string></dict></plist>`))
	if err = z.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// importRequest uploads one IPA under a new id; setUploadId chooses the id instead.
func importRequest(t *testing.T, url, csrf, filename string, raw []byte) *http.Request {
	t.Helper()
	r, err := http.NewRequest("PUT", url+"/api/library/imports/"+db.NewId(), bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filename}))
	r.Header.Set("X-CSRF-Token", csrf)
	return r
}

func setUploadId(r *http.Request, id string) {
	r.URL.Path = "/api/library/imports/" + id
}

func uploadId(r *http.Request) string { return strings.TrimPrefix(r.URL.Path, "/api/library/imports/") }

func doImport(t *testing.T, client *http.Client, r *http.Request, status int) string {
	t.Helper()
	resp, err := client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != status {
		t.Fatalf("import: got %d, want %d: %s", resp.StatusCode, status, raw)
	}
	var result struct{ Id string }
	if status == 201 {
		if err = json.Unmarshal(raw, &result); err != nil || result.Id == "" {
			t.Fatalf("missing imported ID: %s, %v", raw, err)
		}
	}
	return result.Id
}

func TestImportLocalIpaWithoutAppleSession(t *testing.T) {
	dir := t.TempDir()
	h := launch(t, dir, nil)
	defer func() { h.close() }()
	raw := importFixture(t, "test.imported", "Моё приложение")
	id := doImport(t, h.client, importRequest(t, h.http.URL, h.csrf, "../../custom.IPA", raw), 201)
	var page wire.LibraryPage
	h.request(t, "GET", "/api/library", nil, 200, &page)
	if page.Total != 1 || len(page.Items) != 1 {
		t.Fatalf("missing import: %+v", page)
	}
	item := page.Items[0]
	if item.Id != id || item.Name != "Моё приложение" || item.Version != "2.3" || item.Status != "done" || item.Size != int64(len(raw)) || !item.CanManage || item.Icon == "" {
		t.Fatalf("wrong imported metadata: %+v", item)
	}
	h.request(t, "GET", item.Icon, nil, 200, nil)
	var export wire.FileExport
	h.request(t, "POST", "/api/library/export", wire.Selection{Ids: []string{id}}, 201, &export)
	if got := h.request(t, "GET", export.Url, nil, 200, nil); !bytes.Equal(got, raw) {
		t.Fatal("import changed the IPA")
	}
	var share wire.Share
	h.request(t, "POST", "/api/shares", wire.Selection{Ids: []string{id}}, 201, &share)
	var state wire.State
	h.request(t, "GET", "/api/state", nil, 200, &state)
	if state.ShareCount != 1 || h.shares(t)[0].Name != item.Name || h.shares(t)[0].Icon != item.Icon {
		t.Fatalf("share lost the imported app's cached artwork: %+v", h.shares(t))
	}
	if got := h.request(t, "GET", "/packages/"+share.Id+"/"+id+".ipa", nil, 200, nil); !bytes.Equal(got, raw) {
		t.Fatal("hosting changed the imported IPA")
	}
	h.request(t, "POST", "/api/shares/stop", wire.Selection{Ids: []string{share.Id}}, 204, nil)
	if h.artworkCalls.Load() != 0 {
		t.Fatal("import contacted Apple for artwork")
	}
	h.close()
	h = launch(t, dir, h.client)
	h.request(t, "GET", "/api/library", nil, 200, &page)
	if page.Total != 1 || page.Items[0].Id != id {
		t.Fatal("import did not survive restart")
	}
	h.login(t, "first")
	h.request(t, "GET", "/api/library", nil, 200, &page)
	if page.Total != 1 || !page.Items[0].CanManage {
		t.Fatal("local login lost the unowned import")
	}
	h.request(t, "POST", "/api/library/delete", wire.Selection{Ids: []string{id}}, 204, nil)
	if _, err := os.Stat(filepath.Join(dir, "packages", id+".ipa")); !os.IsNotExist(err) {
		t.Fatal("imported file was not deleted", err)
	}
}

func TestImportPublicOwnershipAndSharedLibrary(t *testing.T) {
	for _, shared := range []bool{false, true} {
		t.Run(fmt.Sprintf("shared=%t", shared), func(t *testing.T) {
			app, ts, _ := publicAppWithSharedApps(t, shared)
			owner, other, guest, admin := newBrowser(t, ts), newBrowser(t, ts), newBrowser(t, ts), newBrowser(t, ts)
			raw := importFixture(t, "test.public.import", "Imported App")

			upload := func(b *browser, csrf string, status int) string {
				r := importRequest(t, ts.URL, csrf, "custom.ipa", raw)
				r.Host = publicHost
				r.Header.Set("Origin", "https://"+publicHost)
				// Import uses the browser session, without Apple secrets or a session key.
				return doImport(t, b.client, r, status)
			}

			upload(guest, guest.csrf, 401)
			owner.login("one@example.test", "fixture")
			other.login("two@example.test", "fixture")
			admin.login("root@appleseum", app.ServicePassword())
			upload(owner, "invalid-csrf", 403)
			id := upload(owner, owner.csrf, 201)
			var page wire.LibraryPage
			owner.request("GET", "/api/library", nil, 200, &page)
			if page.Total != 1 || page.Items[0].Id != id || !page.Items[0].CanManage {
				t.Fatal("owner cannot manage import", page)
			}
			icon := page.Items[0].Icon
			other.request("GET", "/api/library?mine=false", nil, 200, &page)
			selection := wire.Selection{Ids: []string{id}}
			if shared {
				if page.Total != 1 || page.Items[0].CanManage {
					t.Fatal("shared import must be read-only for others", page)
				}
				other.request("GET", icon, nil, 200, nil)
				other.request("POST", "/api/library/delete", selection, 403, nil)
				var export wire.FileExport
				other.request("POST", "/api/library/export", selection, 201, &export)
				other.request("GET", export.Url, nil, 200, nil)
				var share wire.Share
				other.request("POST", "/api/shares", selection, 201, &share)
				other.request("POST", "/api/shares/stop", wire.Selection{Ids: []string{share.Id}}, 204, nil)
			} else {
				if page.Total != 0 {
					t.Fatal("private import leaked", page)
				}
				other.request("GET", icon, nil, 404, nil)
				other.request("POST", "/api/library/delete", selection, 404, nil)
				other.request("POST", "/api/library/export", selection, 404, nil)
				other.request("POST", "/api/shares", selection, 404, nil)
			}
			admin.request("GET", "/api/library", nil, 200, &page)
			if page.Total != 1 || !page.Items[0].CanManage {
				t.Fatal("admin lost import access", page)
			}
			serviceId := upload(admin, admin.csrf, 201)
			owner.request("GET", "/api/library?mine=false", nil, 200, &page)
			want := int64(1)
			if shared {
				want = 2
			}
			if page.Total != want {
				t.Fatalf("service upload visibility: got %d, want %d", page.Total, want)
			}
			owner.request("POST", "/api/logout", nil, 204, nil)
			owner.state()
			owner.login("one@example.test", "fixture")
			owner.request("GET", "/api/library?mine=false", nil, 200, &page)
			if page.Total != want {
				t.Fatal("import lost after re-login", page)
			}
			admin.request("POST", "/api/library/delete", wire.Selection{Ids: []string{id, serviceId}}, 204, nil)
		})
	}
}

func TestImportRejectsInvalidFilesAndCleansUp(t *testing.T) {
	dir := t.TempDir()
	h := launch(t, dir, nil)
	defer h.close()
	valid := importFixture(t, "test.reject", "Test")
	for _, tc := range []struct {
		name string
		raw  []byte
	}{
		{"app.zip", valid}, {"app.ipa", []byte("not a zip")},
		{"app.ipa", importFixture(t, "", "Missing bundle")},
		{"app.ipa", importFixture(t, "test.bad", "Bad\nName")},
	} {
		doImport(t, h.client, importRequest(t, h.http.URL, h.csrf, tc.name, tc.raw), 400)
	}
	// A truncated body must not publish an otherwise valid IPA.
	r := importRequest(t, h.http.URL, h.csrf, "valid.ipa", valid)
	body, _ := io.ReadAll(r.Body)
	r.Body = io.NopCloser(bytes.NewReader(body[:len(body)-10]))
	r.ContentLength = int64(len(body) - 10)
	doImport(t, h.client, r, 400)
	// Reject a declared oversized transfer before reading any bytes.
	r = importRequest(t, h.http.URL, h.csrf, "valid.ipa", valid)
	r.ContentLength = 4_296_015_873
	w := httptest.NewRecorder()
	h.app.Handler().ServeHTTP(w, r)
	if w.Code != 413 {
		t.Fatalf("oversized upload: %d %s", w.Code, w.Body.String())
	}
	// A DB failure must roll back both the row and renamed file.
	conn, err := sql.Open("sqlite", filepath.Join(dir, "metadata.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err = conn.Exec("CREATE TRIGGER reject_import BEFORE UPDATE OF size ON packages BEGIN SELECT RAISE(FAIL,'fixture'); END"); err != nil {
		t.Fatal(err)
	}
	doImport(t, h.client, importRequest(t, h.http.URL, h.csrf, "valid.ipa", valid), 500)
	var page wire.LibraryPage
	h.request(t, "GET", "/api/library", nil, 200, &page)
	if page.Total != 5 {
		t.Fatal("failed imports are missing from the transfer list", page)
	}
	for _, item := range page.Items {
		if item.Status != "failed" || item.Error == "" || item.Speed != 0 {
			t.Fatal("rejected upload appears ready or active", item)
		}
	}
	files, err := os.ReadDir(filepath.Join(dir, "packages"))
	if err != nil || len(files) != 0 {
		t.Fatal("rejected upload left files", files, err)
	}
	if _, err = conn.Exec("DROP TRIGGER reject_import"); err != nil {
		t.Fatal(err)
	}
	id := doImport(t, h.client, importRequest(t, h.http.URL, h.csrf, "valid.ipa", valid), 201)
	if strings.Contains(id, "/") {
		t.Fatal("unsafe package ID", id)
	}
}

type importResponse struct {
	status int
	body   []byte
	err    error
}

// Keep the upload open to exercise progress and cancellation over HTTP.
func streamingImport(t *testing.T, client *http.Client, request *http.Request) (*io.PipeWriter, []byte, <-chan importResponse) {
	t.Helper()
	body, err := io.ReadAll(request.Body)
	if err != nil || len(body) < 16_384 {
		t.Fatal("stream fixture is too short", err)
	}
	reader, writer := io.Pipe()
	request.Body, request.GetBody = reader, nil
	result := make(chan importResponse, 1)
	go func() {
		response, err := client.Do(request)
		if err != nil {
			result <- importResponse{err: err}
			return
		}
		defer response.Body.Close()
		data, err := io.ReadAll(response.Body)
		result <- importResponse{status: response.StatusCode, body: data, err: err}
	}()
	t.Cleanup(func() { writer.Close(); reader.Close() })
	if _, err = writer.Write(body[:8_192]); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if _, err = writer.Write(body[8_192:16_384]); err != nil {
		t.Fatal(err)
	}
	return writer, body[16_384:], result
}

func awaitImportProgress(t *testing.T, read func() wire.LibraryPage) wire.LibraryItem {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		page := read()
		if len(page.Items) == 1 {
			item := page.Items[0]
			if item.Status == "downloading" && item.Received > 0 && item.Received < item.Total && item.Speed > 0 {
				return item
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("upload progress or speed never appeared in the library")
	return wire.LibraryItem{}
}

func TestImportProgressUsesLibraryTransfers(t *testing.T) {
	dir := t.TempDir()
	h := launch(t, dir, nil)
	defer h.close()
	// An import must also work after a local Apple sign-out.
	h.login(t, "first")
	h.request(t, "POST", "/api/logout", nil, 204, nil)
	raw := importFixture(t, "test.progress", "Uploaded App", true)
	writer, remaining, result := streamingImport(t, h.client, importRequest(t, h.http.URL, h.csrf, "existing.ipa", raw))

	read := func() wire.LibraryPage {
		var page wire.LibraryPage
		h.request(t, "GET", "/api/library", nil, 200, &page)
		return page
	}

	item := awaitImportProgress(t, read)
	if item.Name != "existing.ipa" || item.Total != int64(len(raw)) || !item.CanManage {
		t.Fatal("wrong transfer metadata", item)
	}
	var state wire.State
	h.request(t, "GET", "/api/state", nil, 200, &state)
	if !state.DownloadsActive {
		t.Fatal("local guest upload does not activate polling")
	}
	selection := wire.Selection{Ids: []string{item.Id}}
	h.request(t, "POST", "/api/library/export", selection, 409, nil)
	h.request(t, "POST", "/api/shares", selection, 409, nil)
	if _, err := writer.Write(remaining); err != nil {
		t.Fatal(err)
	}
	writer.Close()
	response := <-result
	if response.err != nil || response.status != 201 {
		t.Fatalf("upload failed: %+v", response)
	}
	page := read()
	if len(page.Items) != 1 || page.Items[0].Id != item.Id || page.Items[0].Status != "done" || page.Items[0].Name != "Uploaded App" || page.Items[0].Speed != 0 || page.Items[0].Size != int64(len(raw)) {
		t.Fatal("transfer did not become the ready library entry", page)
	}
	h.request(t, "GET", "/api/state", nil, 200, &state)
	if state.DownloadsActive {
		t.Fatal("completed upload kept polling active")
	}
}

func TestImportCanBeCanceledByLocalGuest(t *testing.T) {
	dir := t.TempDir()
	h := launch(t, dir, nil)
	defer h.close()
	raw := importFixture(t, "test.cancel", "Canceled App", true)
	r := importRequest(t, h.http.URL, h.csrf, "cancel.ipa", raw)
	setUploadId(r, "01900000-0000-7000-8000-000000000002")
	writer, _, result := streamingImport(t, h.client, r)
	item := awaitImportProgress(t, func() wire.LibraryPage {
		var page wire.LibraryPage
		h.request(t, "GET", "/api/library", nil, 200, &page)
		return page
	})
	if item.Id != uploadId(r) {
		t.Fatal("browser transfer ID was not preserved", item.Id)
	}
	duplicate := importRequest(t, h.http.URL, h.csrf, "duplicate.ipa", raw)
	setUploadId(duplicate, item.Id)
	doImport(t, h.client, duplicate, 409)
	h.request(t, "POST", "/api/downloads/"+item.Id+"/cancel", nil, 202, nil)
	writer.Close()
	<-result
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var state wire.State
		h.request(t, "GET", "/api/state", nil, 200, &state)
		if !state.DownloadsActive {
			break
		}
		time.Sleep(time.Millisecond)
	}
	var page wire.LibraryPage
	h.request(t, "GET", "/api/library", nil, 200, &page)
	if page.Total != 0 {
		t.Fatal("canceled upload was published", page)
	}
	files, err := os.ReadDir(filepath.Join(dir, "packages"))
	if err != nil || len(files) != 0 {
		t.Fatal("canceled upload left a file", files, err)
	}
}

func TestImportProgressPublicIsolationAndAdminCancellation(t *testing.T) {
	for _, shared := range []bool{false, true} {
		t.Run(fmt.Sprint(shared), func(t *testing.T) {
			app, ts, dir := publicAppWithSharedApps(t, shared)
			owner, other, admin := newBrowser(t, ts), newBrowser(t, ts), newBrowser(t, ts)
			owner.login("one@example.test", "fixture")
			other.login("two@example.test", "fixture")
			admin.login("root@appleseum", app.ServicePassword())
			raw := importFixture(t, "test.public.progress", "Public Upload", true)
			r := importRequest(t, ts.URL, owner.csrf, "public.ipa", raw)
			r.Host = publicHost
			r.Header.Set("Origin", "https://"+publicHost)
			writer, _, result := streamingImport(t, owner.client, r)
			item := awaitImportProgress(t, func() wire.LibraryPage {
				var page wire.LibraryPage
				owner.request("GET", "/api/library", nil, 200, &page)
				return page
			})
			var page wire.LibraryPage
			other.request("GET", "/api/library?mine=false", nil, 200, &page)
			if page.Total != 0 || other.state().DownloadsActive {
				t.Fatal("incomplete upload leaked to another account", page)
			}
			other.request("POST", "/api/downloads/"+item.Id+"/cancel", nil, 404, nil)
			admin.request("GET", "/api/library", nil, 200, &page)
			if len(page.Items) != 1 || page.Items[0].Id != item.Id || !page.Items[0].CanManage {
				t.Fatal("admin cannot inspect upload", page)
			}
			admin.request("POST", "/api/downloads/"+item.Id+"/cancel", nil, 202, nil)
			writer.Close()
			<-result
			deadline := time.Now().Add(2 * time.Second)
			for owner.state().DownloadsActive && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			owner.request("GET", "/api/library", nil, 200, &page)
			if page.Total != 0 {
				t.Fatal("canceled public import was published", page)
			}
			files, err := os.ReadDir(filepath.Join(dir, "packages"))
			if err != nil || len(files) != 0 {
				t.Fatal("public cancellation left files", files, err)
			}
		})
	}
}

func TestImportBrowserIdCannotOverwriteSavedPackages(t *testing.T) {
	dir := t.TempDir()
	h := launch(t, dir, nil)
	defer h.close()
	raw := importFixture(t, "test.browser.id", "Browser Upload")
	r := importRequest(t, h.http.URL, h.csrf, "existing.ipa", raw)
	setUploadId(r, "01900000-0000-7000-8000-000000000002")
	id := doImport(t, h.client, r, 201)
	if id != uploadId(r) {
		t.Fatal("unexpected upload ID", id)
	}
	r = importRequest(t, h.http.URL, h.csrf, "replacement.ipa", importFixture(t, "test.replacement", "Wrong App"))
	setUploadId(r, id)
	doImport(t, h.client, r, 409)
	r = importRequest(t, h.http.URL, h.csrf, "invalid.ipa", raw)
	setUploadId(r, "not-an-id")
	doImport(t, h.client, r, 400)
	var page wire.LibraryPage
	h.request(t, "GET", "/api/library", nil, 200, &page)
	if page.Total != 1 || page.Items[0].Name != "Browser Upload" {
		t.Fatal("upload ID replaced another package", page)
	}
	stored, err := os.ReadFile(filepath.Join(dir, "packages", id+".ipa"))
	if err != nil || !bytes.Equal(stored, raw) {
		t.Fatal("saved IPA was overwritten", err)
	}
}
