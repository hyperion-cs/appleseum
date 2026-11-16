package integration_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hyperion-cs/appleseum/applekit"
	"howett.net/plist"

	"appleseum/backend/internal/account"
	server "appleseum/backend/internal/app"
	"appleseum/backend/internal/db"
	"appleseum/backend/internal/wire"
)

const (
	publicHost = "apps.example.test:9443"
)

type browser struct {
	t      *testing.T
	server *httptest.Server
	client *http.Client
	key    string
	csrf   string
}

func publicApp(t *testing.T) (*server.App, *httptest.Server, string) {
	t.Helper()
	return publicAppWithSharedApps(t, false)
}

func publicAppWithSharedApps(t *testing.T, shared bool) (*server.App, *httptest.Server, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "packages"), 0700); err != nil {
		t.Fatal(err)
	}
	app, err := newTestServer(server.Config{DataDir: dir, Public: true, SharedLibrary: shared, Admin: true,
		Apple: &testApple{signIn: func(_ context.Context, login, password, code string) (account.Session, error) {
			return &fakeApple{owner: strings.Split(login, "@")[0]}, nil
		}, open: func(record db.Identity) (account.Session, error) {
			return &fakeApple{owner: record.Owner}, nil
		}},
		Artwork: func(context.Context, string) ([]byte, error) { return testIcon(), nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewTLSServer(app.Handler())
	t.Cleanup(func() { ts.Close(); app.Close() })
	return app, ts, dir
}

func newBrowser(t *testing.T, ts *httptest.Server) *browser {
	t.Helper()
	copy := *ts.Client()
	client := &copy
	jar, _ := cookiejar.New(nil)
	client.Jar = jar
	client.Timeout = 5 * time.Second
	key := make([]byte, 32)
	rand.Read(key)
	b := &browser{t: t, server: ts, client: client, key: base64.RawURLEncoding.EncodeToString(key)}
	b.state()
	return b
}

func (b *browser) request(method, path string, input any, status int, target any) []byte {
	b.t.Helper()
	var body []byte
	if input != nil {
		body, _ = json.Marshal(input)
	}
	r, err := http.NewRequest(method, b.server.URL+path, bytes.NewReader(body))
	if err != nil {
		b.t.Fatal(err)
	}
	r.Host = publicHost
	r.Header.Set("Origin", "https://"+publicHost)
	r.Header.Set("X-CSRF-Token", b.csrf)
	if b.key != "" {
		r.Header.Set("X-Session-Key", b.key)
	}
	if input != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	resp, err := b.client.Do(r)
	if err != nil {
		b.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		b.t.Fatal(err)
	}
	if resp.StatusCode != status {
		b.t.Fatalf("%s %s: got %d, want %d: %s", method, path, resp.StatusCode, status, raw)
	}
	if target != nil && len(raw) > 0 {
		if err = json.Unmarshal(raw, target); err != nil {
			b.t.Fatal(err)
		}
	}
	return raw
}

func (b *browser) state() wire.State {
	b.t.Helper()
	var state wire.State
	b.request("GET", "/api/state", nil, 200, &state)
	b.csrf = state.Csrf
	return state
}

func (b *browser) login(login, password string) {
	b.request("POST", "/api/login", wire.LoginRequest{Email: login, Password: password, Remember: true}, 200, nil)
}

func (b *browser) refreshCatalog() {
	b.t.Helper()
	b.request("POST", "/api/catalog/refresh", nil, 202, nil)
	deadline := time.Now().Add(5 * time.Second)
	for b.state().Catalog.Status == "loading" {
		if time.Now().After(deadline) {
			b.t.Fatal("catalog did not finish")
		}
		time.Sleep(time.Millisecond)
	}
}

func (b *browser) download(id string) wire.LibraryItem {
	b.t.Helper()
	b.refreshCatalog()
	b.request("POST", "/api/downloads", wire.DownloadRequest{Selection: wire.Selection{Ids: []string{id}}}, 202, nil)
	deadline := time.Now().Add(5 * time.Second)
	for {
		var page wire.LibraryPage
		b.request("GET", "/api/library", nil, 200, &page)
		for _, item := range page.Items {
			if item.AppId == id && item.Status == "done" {
				return item
			}
		}
		if time.Now().After(deadline) {
			b.t.Fatalf("download did not finish: %+v", page.Items)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestPublicBrowserIsolationAndSharedLibraryRights(t *testing.T) {
	app, ts, dir := publicAppWithSharedApps(t, true)
	one, two, root := newBrowser(t, ts), newBrowser(t, ts), newBrowser(t, ts)
	one.login("one@example.test", "synthetic-one")
	if state := two.state(); state.Account != nil || !state.Mode.Public {
		t.Fatal("another browser received the Apple session")
	}
	two.login("two@example.test", "synthetic-two")
	one.request("PUT", "/api/preferences", wire.PreferencesInput{Language: "ru", Theme: "light"}, 200, nil)
	if two.state().Preferences.Language != "en" {
		t.Fatal("preferences leaked across browsers")
	}
	item := one.download("1")
	var page wire.LibraryPage
	two.request("GET", "/api/library?mine=false", nil, 200, &page)
	if len(page.Items) != 1 || page.Items[0].Id != item.Id || page.Items[0].CanManage {
		t.Fatal("shared IPA permissions are wrong", page)
	}
	two.request("POST", "/api/library/delete", wire.Selection{Ids: []string{item.Id}}, 403, nil)
	root.login("root@appleseum", app.ServicePassword())
	state := root.state()
	if !state.Mode.Service || state.Account != nil || state.Mode.Admin || state.Mode.Debug {
		t.Fatal("root acquired an Apple profile or settings")
	}
	root.request("GET", "/api/catalog", nil, 401, nil)
	// The service account has no device; its settings do not exist for it.
	root.request("GET", "/api/admin/device", nil, 404, nil)
	root.request("POST", "/api/library/delete", wire.Selection{Ids: []string{item.Id}}, 204, nil)
	if _, err := os.Stat(filepath.Join(dir, "local.key")); !os.IsNotExist(err) {
		t.Fatal("public mode created a local encryption key")
	}
	var raw []byte
	if err := deviceDb(t, dir).QueryRow("SELECT secrets FROM sessions WHERE secrets IS NOT NULL LIMIT 1").Scan(&raw); err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{"synthetic-one", "synthetic-two", "session-secret-marker"} {
		if bytes.Contains(raw, []byte(marker)) {
			t.Fatal("plaintext Apple secret in SQLite")
		}
	}
}

func TestPublicManifestUrlsAndMultipleShares(t *testing.T) {
	_, ts, _ := publicAppWithSharedApps(t, true)
	one, two := newBrowser(t, ts), newBrowser(t, ts)
	one.login("one@example.test", "fixture")
	two.login("two@example.test", "fixture")
	item := one.download("1")
	var first, second wire.Share
	one.request("POST", "/api/shares", wire.Selection{Ids: []string{item.Id}}, 201, &first)
	rawDuplicate := two.request("POST", "/api/shares", wire.Selection{Ids: []string{item.Id}}, 409, nil)
	if !bytes.Contains(rawDuplicate, []byte("sharing_duplicate")) {
		t.Fatal("duplicate share was not rejected")
	}
	rawDuplicate = one.request("POST", "/api/shares", wire.Selection{Ids: []string{item.Id}}, 409, nil)
	if !bytes.Contains(rawDuplicate, []byte("sharing_duplicate")) {
		t.Fatal("creator's duplicate share was not rejected")
	}
	other := two.download("2")
	two.request("POST", "/api/shares", wire.Selection{Ids: []string{item.Id, other.Id}}, 201, &second)
	two.request("POST", "/api/shares/stop", wire.Selection{Ids: []string{first.Id}}, 404, nil)
	if first.Id == second.Id || first.Count != 1 || second.Count != 2 {
		t.Fatal("overlapping sets must remain independent shares")
	}
	if one.state().ShareCount != 1 || two.state().ShareCount != 1 {
		t.Fatal("deduplication changed share visibility or ownership")
	}
	link, err := url.Parse(first.Url)
	if err != nil {
		t.Fatal(err)
	}
	manifestUrl, err := url.Parse(link.Query().Get("url"))
	if err != nil || manifestUrl.Scheme != "https" || manifestUrl.Host != publicHost {
		t.Fatal("wrong public manifest URL", manifestUrl, err)
	}
	r, err := http.NewRequest("GET", ts.URL+manifestUrl.Path, nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Host = publicHost
	r.Header.Set("X-Forwarded-Proto", "http")
	resp, err := ts.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var manifest struct {
		Items []struct {
			Assets []struct {
				Url string `plist:"url"`
			} `plist:"assets"`
		} `plist:"items"`
	}
	if _, err = plist.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	ipaUrl, err := url.Parse(manifest.Items[0].Assets[0].Url)
	if err != nil || ipaUrl.Scheme != "https" || ipaUrl.Host != publicHost {
		t.Fatal("wrong public IPA URL", ipaUrl, err)
	}
	r, _ = http.NewRequest("GET", ts.URL+ipaUrl.Path, nil)
	r.Host = publicHost
	r.Header.Set("Range", "bytes=0-9")
	resp, err = ts.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 206 || len(raw) != 10 {
		t.Fatal("cookie-free IPA range download failed", resp.StatusCode, len(raw))
	}
}

func TestPublicSessionExpiryAndMissingKey(t *testing.T) {
	_, ts, dir := publicApp(t)
	b := newBrowser(t, ts)
	b.login("one@example.test", "fixture")
	b.key = ""
	if b.state().Account == nil {
		t.Fatal("missing encryption key erased account metadata")
	}
	b.request("POST", "/api/catalog/refresh", nil, 401, nil)
	if _, err := deviceDb(t, dir).Exec("UPDATE sessions SET expires_at=? WHERE scope='public'", time.Now().Add(-time.Minute).Unix()); err != nil {
		t.Fatal(err)
	}
	if b.state().Account != nil {
		t.Fatal("expired cookie restored Apple access")
	}
}

func TestPublicLocalActionsDoNotNeedSessionKey(t *testing.T) {
	_, ts, dir := publicApp(t)
	b := newBrowser(t, ts)
	b.login("one@example.test", "fixture")
	item := b.download("1")
	var sealed []byte
	conn := deviceDb(t, dir)
	if err := conn.QueryRow("SELECT secrets FROM sessions WHERE secrets IS NOT NULL LIMIT 1").Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	b.key = ""
	b.state()
	b.request("GET", "/api/catalog", nil, 200, nil)
	b.request("GET", "/api/library", nil, 200, nil)
	b.request("GET", "/api/profile", nil, 200, nil)
	var device wire.DeviceProfile
	b.request("GET", "/api/admin/device", nil, 200, &device)
	device.Settings.Locale, device.Settings.TimeZone = "ru-KZ", "Asia/Almaty"
	b.request("PUT", "/api/admin/device", device.Settings, 200, nil)
	var share wire.Share
	b.request("POST", "/api/shares", wire.Selection{Ids: []string{item.Id}}, 201, &share)
	b.request("POST", "/api/shares/stop", wire.Selection{Ids: []string{share.Id}}, 204, nil)
	var saved []byte
	if err := conn.QueryRow("SELECT secrets FROM sessions WHERE secrets IS NOT NULL LIMIT 1").Scan(&saved); err != nil || !bytes.Equal(saved, sealed) {
		t.Fatal("local operations rewrote Apple secrets", err)
	}
	for _, path := range []string{"/api/catalog/refresh", "/api/profile/refresh", "/api/downloads"} {
		b.request("POST", path, wire.Selection{Ids: []string{"1"}}, 401, nil)
	}
	b.request("POST", "/api/admin/device/reset", nil, 200, nil)
	if b.state().Account != nil {
		t.Fatal("keyless device reset retained the Apple session")
	}
}

type datedApple struct {
	fakeApple
	authenticated time.Time
}

func (c *datedApple) SessionParameters() applekit.SessionParameters {
	p := c.fakeApple.SessionParameters()
	p.AuthenticatedAt, p.LastSuccessAt = c.authenticated, c.authenticated
	return p
}

func TestPublicAliasesChooseNewestDeviceAndResetOnlyRelatedSessions(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "packages"), 0700); err != nil {
		t.Fatal(err)
	}
	app, err := newTestServer(server.Config{DataDir: dir, Public: true, Admin: true, Apple: &testApple{signIn: func(_ context.Context, login, password, code string) (account.Session, error) {
		if password == "wrong" {
			return nil, applekit.ErrCredentialsRejected
		}
		return &datedApple{fakeApple: fakeApple{owner: "unified"}, authenticated: time.Now()}, nil
	}}})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewTLSServer(app.Handler())
	defer func() { ts.Close(); app.Close() }()
	one, two, three, unknown := newBrowser(t, ts), newBrowser(t, ts), newBrowser(t, ts), newBrowser(t, ts)
	one.login("person@example.test", "fixture")
	var first, second, latest wire.DeviceProfile
	one.request("GET", "/api/admin/device", nil, 200, &first)
	two.request("GET", "/api/admin/device", nil, 200, &second)
	second.Settings.Model, second.Settings.OsVersion = "MacBookAir9,1", "15.7"
	two.request("PUT", "/api/admin/device", second.Settings, 200, &second)
	two.login("79990000000", "fixture")
	three.login("person@example.test", "fixture")
	three.request("GET", "/api/admin/device", nil, 200, &latest)
	if latest.Id != second.Id || latest.Id == first.Id || latest.Settings != second.Settings {
		t.Fatal("aliases did not select the latest whole device")
	}
	one.request("GET", "/api/admin/device", nil, 200, &latest)
	if latest.Id != first.Id {
		t.Fatal("existing Apple session switched devices before reauthentication")
	}
	unknown.request("POST", "/api/login", wire.LoginRequest{Email: "person@example.test", Password: "wrong"}, 401, nil)
	unknown.request("GET", "/api/admin/device", nil, 200, &latest)
	if !latest.ReadOnly || latest.Id != second.Id {
		t.Fatal("unproven alias exposes editable account device")
	}
	unknown.request("PUT", "/api/admin/device", latest.Settings, 403, nil)
	unknown.request("POST", "/api/admin/device/reset", nil, 403, nil)
	unknown.request("POST", "/api/login", wire.LoginRequest{Email: "unknown@example.test", Password: "wrong"}, 401, nil)
	unknown.request("GET", "/api/admin/device", nil, 200, &latest)
	if latest.ReadOnly || latest.Id == second.Id {
		t.Fatal("another unknown login inherited the known account device")
	}
	latest.Settings.TimeZone = "Europe/London"
	unknown.request("PUT", "/api/admin/device", latest.Settings, 200, nil)
	three.request("POST", "/api/admin/device/reset", nil, 200, &latest)
	if latest.Id == second.Id || !latest.ReadOnly {
		t.Fatal("device reset failed")
	}
	if two.state().Account != nil || three.state().Account != nil || one.state().Account == nil {
		t.Fatal("device reset ended the wrong Apple sessions")
	}
	var count int
	if err = deviceDb(t, dir).QueryRow("SELECT count(*) FROM account_logins WHERE account_id='unified'").Scan(&count); err != nil || count != 3 {
		t.Fatal("confirmed aliases lost", count, err)
	}
}

func TestPublicRestartRestoresAppleButInvalidatesTemporaryRoot(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "packages"), 0700); err != nil {
		t.Fatal(err)
	}
	cfg := server.Config{DataDir: dir, Public: true, Apple: &testApple{signIn: func(context.Context, string, string, string) (account.Session, error) {
		return &fakeApple{owner: "returning"}, nil
	}, open: func(r db.Identity) (account.Session, error) { return &fakeApple{owner: r.Owner}, nil }}}
	app, err := newTestServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewTLSServer(app.Handler())
	defer func() { ts.Close(); app.Close() }()
	user, root := newBrowser(t, ts), newBrowser(t, ts)
	user.login("person@example.test", "fixture")
	item := user.download("1")
	root.login("root@appleseum", app.ServicePassword())
	oldPassword := app.ServicePassword()
	ts.Close()
	if err = app.Close(); err != nil {
		t.Fatal(err)
	}
	app, err = newTestServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ts = httptest.NewTLSServer(app.Handler())
	// Cookie scopes remain the same host; httptest may allocate another port.
	for _, b := range []*browser{user, root} {
		oldUrl, _ := url.Parse(b.server.URL)
		newUrl, _ := url.Parse(ts.URL)
		b.client.Jar.SetCookies(newUrl, b.client.Jar.Cookies(oldUrl))
		copy := *ts.Client()
		copy.Jar = b.client.Jar
		b.client = &copy
		b.server = ts
	}
	if user.state().Account == nil || root.state().Mode.Service || root.state().Account != nil || app.ServicePassword() == oldPassword {
		t.Fatal("restart restored the wrong authority")
	}
	root.request("POST", "/api/library/delete", wire.Selection{Ids: []string{item.Id}}, 401, nil)
	another := newBrowser(t, ts)
	another.login("person@example.test", "fixture")
	var page wire.LibraryPage
	another.request("GET", "/api/library", nil, 200, &page)
	if len(page.Items) != 1 || !page.Items[0].CanManage {
		t.Fatal("new browser lost confirmed account ownership")
	}
	another.request("POST", "/api/library/delete", wire.Selection{Ids: []string{item.Id}}, 204, nil)
}

func TestPublicRootCancelsForeignDownloadAndStopsForeignShare(t *testing.T) {
	app, ts, _ := publicApp(t)
	user, other, root := newBrowser(t, ts), newBrowser(t, ts), newBrowser(t, ts)
	user.login("one@example.test", "fixture")
	other.login("two@example.test", "fixture")
	item := user.download("1")
	var share wire.Share
	user.request("POST", "/api/shares", wire.Selection{Ids: []string{item.Id}}, 201, &share)
	user.request("POST", "/api/downloads", wire.DownloadRequest{Selection: wire.Selection{Ids: []string{"32"}}}, 202, nil)
	var jobs wire.LibraryPage
	user.request("GET", "/api/library", nil, 200, &jobs)
	id := ""
	for _, j := range jobs.Items {
		if j.AppId == "32" {
			id = j.Id
		}
	}
	if id == "" {
		t.Fatal("missing queued job")
	}
	other.request("POST", "/api/downloads/"+id+"/cancel", nil, 404, nil)
	root.login("root@appleseum", app.ServicePassword())
	root.request("POST", "/api/downloads/"+id+"/cancel", nil, 202, nil)
	root.request("POST", "/api/shares/stop", wire.Selection{Ids: []string{share.Id}}, 204, nil)
	root.request("POST", "/api/library/delete", wire.Selection{Ids: []string{item.Id}}, 204, nil)
	deadline := time.Now().Add(3 * time.Second)
	for {
		user.request("GET", "/api/library", nil, 200, &jobs)
		// A canceled transfer leaves the library.
		if !slices.ContainsFunc(jobs.Items, func(j wire.LibraryItem) bool { return j.Id == id }) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("root did not cancel the author's worker", jobs)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestPublicLibraryVisibility(t *testing.T) {
	for _, shared := range []bool{false, true} {
		name := "own apps by default"
		if shared {
			name = "include shared apps"
		}
		t.Run(name, func(t *testing.T) {
			app, ts, dir := publicAppWithSharedApps(t, shared)
			one, two, same, root := newBrowser(t, ts), newBrowser(t, ts), newBrowser(t, ts), newBrowser(t, ts)
			one.login("one@example.test", "fixture")
			two.login("two@example.test", "fixture")
			same.login("one@example.test", "fixture")
			item := one.download("1")
			one.request("POST", "/api/downloads", wire.DownloadRequest{Selection: wire.Selection{Ids: []string{"32"}}}, 202, nil)

			var own wire.LibraryPage
			same.request("GET", "/api/library", nil, 200, &own)
			if own.Total != 2 {
				t.Fatal("another browser of the same account lost its files or jobs", own)
			}
			var page wire.LibraryPage
			two.request("GET", "/api/library?limit=1&q=App&mine=false", nil, 200, &page)
			expected := int64(0)
			if shared {
				expected = 1
			}
			if page.Total != expected || int64(len(page.Items)) != expected || page.Next {
				t.Fatal("foreign library files, counts or jobs have wrong visibility", page)
			}
			var selected wire.LibrarySelectionResult
			two.request("POST", "/api/library/selection", wire.Selection{Filter: &wire.Filter{Query: "App"}}, 200, &selected)
			if int64(len(selected.Ids)) != expected || int64(len(selected.ReadyIds)) != expected || len(selected.ManageIds) != 0 {
				t.Fatal("select-all does not match library visibility", selected)
			}
			selectionStatus, fileStatus := 400, 404
			if shared {
				selectionStatus, fileStatus = 200, 201
			}
			two.request("POST", "/api/library/selection", wire.Selection{Ids: []string{item.Id}}, selectionStatus, nil)
			var exported wire.FileExport
			two.request("POST", "/api/library/export", wire.Selection{Ids: []string{item.Id}}, fileStatus, &exported)
			var share wire.Share
			two.request("POST", "/api/shares", wire.Selection{Ids: []string{item.Id}}, fileStatus, &share)
			if shared {
				two.request("GET", exported.Url, nil, 200, nil)
				two.request("POST", "/api/shares/stop", wire.Selection{Ids: []string{share.Id}}, 204, nil)
				two.request("POST", "/api/library/delete", wire.Selection{Ids: []string{item.Id}}, 403, nil)
			}

			two.refreshCatalog()
			var catalog wire.CatalogPage
			two.request("GET", "/api/catalog?sort=size&limit=128", nil, 200, &catalog)
			found := false
			for _, entry := range catalog.Items {
				if entry.Id == "1" {
					found = true
					size := int64(1_000_000)
					if shared {
						size = item.Size
					}
					if entry.InLibrary != shared || entry.Size == nil || *entry.Size != size {
						t.Fatal("catalog used a hidden IPA", entry)
					}
				}
			}
			if !found {
				t.Fatal("catalog lost the app")
			}
			var downloadable wire.SelectionResult
			two.request("POST", "/api/catalog/selection", wire.Selection{Ids: []string{"1"}}, 200, &downloadable)
			if len(downloadable.Ids) != 1-int(expected) {
				t.Fatal("foreign IPA blocked a private download", downloadable)
			}
			if shared {
				two.request("POST", "/api/downloads", wire.DownloadRequest{Selection: wire.Selection{Ids: []string{"1"}}}, 202, nil)
				requireAppCopies(t, dir, 1, 1, 1)
			} else {
				ownItem := two.download("1")
				if ownItem.Id == item.Id || !ownItem.CanManage {
					t.Fatal("private account did not get its own IPA", ownItem)
				}
				requireAppCopies(t, dir, 1, 2, 2)
			}
			root.login("root@appleseum", app.ServicePassword())
			root.request("GET", "/api/library?limit=128", nil, 200, &page)
			if page.Total != 3-expected {
				t.Fatal("root cannot see the whole library", page)
			}
			for _, entry := range page.Items {
				if !entry.CanManage {
					t.Fatal("root lost management rights", entry)
				}
			}
		})
	}
}

func TestPublicLibraryMineFilter(t *testing.T) {
	app, ts, _ := publicAppWithSharedApps(t, true)
	one, two, same, root := newBrowser(t, ts), newBrowser(t, ts), newBrowser(t, ts), newBrowser(t, ts)
	one.login("one@example.test", "fixture")
	two.login("two@example.test", "fixture")
	same.login("one@example.test", "fixture")
	first := one.download("1")
	foreign := two.download("2")
	last := one.download("3")
	if state := one.state(); !state.Mode.SharedLibrary || !state.Tables.Library.Value.Mine || false {
		t.Fatal("shared library filter defaults are wrong", state.Tables)
	}
	for _, browser := range []*browser{one, same} {
		var page wire.LibraryPage
		browser.request("GET", "/api/library", nil, 200, &page)
		if page.Total != 2 || len(page.Items) != 2 {
			t.Fatal("default filter does not show the current account's files", page)
		}
		for _, item := range page.Items {
			if !item.CanManage || item.Id == foreign.Id {
				t.Fatal("default filter included another account", page)
			}
		}
	}
	var page wire.LibraryPage
	one.request("GET", "/api/library?mine=false", nil, 200, &page)
	if page.Total != 3 || len(page.Items) != 3 {
		t.Fatal("cannot show shared files", page)
	}
	one.request("GET", "/api/library?mine=true&q=App%2002", nil, 200, &page)
	if page.Total != 0 || len(page.Items) != 0 {
		t.Fatal("search count included another account", page)
	}
	one.request("GET", "/api/library?mine=false&q=App%2002", nil, 200, &page)
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].Id != foreign.Id {
		t.Fatal("shared search lost its result", page)
	}
	one.request("GET", "/api/library?mine=true&limit=1&sort=name", nil, 200, &page)
	if page.Total != 2 || len(page.Items) != 1 || page.Items[0].Id != first.Id || !page.Next {
		t.Fatal("wrong first owned page", page)
	}
	firstNext := page.Items[len(page.Items)-1].Cursor
	one.request("GET", "/api/library?mine=false&limit=1&sort=name&cursor="+url.QueryEscape(firstNext), nil, 200, nil)
	one.request("GET", "/api/library?mine=true&limit=1&sort=name&cursor="+url.QueryEscape(firstNext), nil, 200, &page)
	if page.Total != 2 || len(page.Items) != 1 || page.Items[0].Id != last.Id || page.Next || !page.Previous {
		t.Fatal("owned pagination included a foreign file", page)
	}
	one.request("GET", "/api/library?mine=true&limit=1&sort=name&cursor="+url.QueryEscape(page.Items[0].Cursor)+"&move=prev", nil, 200, &page)
	if len(page.Items) != 1 || page.Items[0].Id != first.Id {
		t.Fatal("owned backward pagination is wrong", page)
	}
	for _, mine := range []bool{true, false} {
		var selected wire.LibrarySelectionResult
		one.request("POST", "/api/library/selection", wire.Selection{Filter: &wire.Filter{Mine: mine}}, 200, &selected)
		expected := 3
		if mine {
			expected = 2
		}
		if len(selected.Ids) != expected || len(selected.ReadyIds) != expected || len(selected.ManageIds) != 2 {
			t.Fatal("select-all ignored ownership filter", selected)
		}
		one.request("POST", "/api/library/selection", wire.Selection{Filter: &wire.Filter{Query: "App 02", Mine: mine}}, 200, &selected)
		expected -= 2
		if len(selected.Ids) != expected {
			t.Fatal("select-all search ignored ownership filter", selected)
		}
	}
	input := wire.LibraryView{Sort: "createdAt", Direction: "desc", Mine: false}
	one.request("PUT", "/api/table-settings/library", input, 200, nil)
	if one.state().Tables.Library.Value.Mine || !two.state().Tables.Library.Value.Mine || !same.state().Tables.Library.Value.Mine {
		t.Fatal("filter preference did not save independently per browser")
	}
	input.Mine = true
	one.request("PUT", "/api/table-settings/library", input, 200, nil)
	if !one.state().Tables.Library.Value.Mine {
		t.Fatal("cannot restore only mine preference")
	}
	for _, path := range []string{"/api/library?mine=1", "/api/library?mine=", "/api/library?mine=true&mine=false", "/api/catalog?mine=true"} {
		one.request("GET", path, nil, 400, nil)
	}
	one.request("PUT", "/api/table-settings/catalog", map[string]any{"sort": "name", "direction": "asc", "mine": true}, 400, nil)
	one.request("POST", "/api/catalog/selection", wire.Selection{Filter: &wire.Filter{Mine: true}}, 400, nil)
	root.login("root@appleseum", app.ServicePassword())
	root.request("GET", "/api/library?mine=true", nil, 200, &page)
	if page.Total != 3 {
		t.Fatal("service account lost access to the whole library", page)
	}
}
