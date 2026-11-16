package integration_test

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hyperion-cs/appleseum/applekit"
	"howett.net/plist"

	"appleseum/backend/internal/account"
	server "appleseum/backend/internal/app"
	"appleseum/backend/internal/db"
	"appleseum/backend/internal/wire"
)

type fakeApple struct{ owner string }

// signedInAt is the account's sign-in time, or "" when nobody is signed in.
func signedInAt(state wire.State) string {
	if state.Account == nil {
		return ""
	}
	return state.Account.SignedInAt
}

// testApple stands in for Apple: signIn plays a sign-in, open reopens a saved session.
type testApple struct {
	signIn func(ctx context.Context, login, password, code string) (account.Session, error)
	open   func(db.Identity) (account.Session, error)
}

func (f *testApple) SignIn(ctx context.Context, _ *applekit.Authenticator, login, password, code string) (account.Session, error) {
	return f.signIn(ctx, login, password, code)
}

func (f *testApple) Open(_ *applekit.Authenticator, identity db.Identity, _ applekit.SessionSecrets, _ *http.Client) (account.Session, error) {
	return f.open(identity)
}

// newTestServer reopens a saved session with the synthetic one that signed in, unless the test says otherwise.
func newTestServer(cfg server.Config) (*server.App, error) {
	if fake, ok := cfg.Apple.(*testApple); ok && fake.open == nil {
		original := fake.signIn
		var mu sync.Mutex
		sessions := map[string]account.Session{}
		fake = &testApple{signIn: func(ctx context.Context, login, password, code string) (account.Session, error) {
			session, err := original(ctx, login, password, code)
			if err == nil && session != nil {
				mu.Lock()
				sessions[session.OwnerId()] = session
				mu.Unlock()
			}
			return session, err
		}, open: func(identity db.Identity) (account.Session, error) {
			mu.Lock()
			defer mu.Unlock()
			if session := sessions[identity.Owner]; session != nil {
				return session, nil
			}
			return nil, fmt.Errorf("synthetic session not registered")
		}}
		cfg.Apple = fake
	}
	return server.New(cfg)
}

func (f *fakeApple) Account() applekit.Account {
	return applekit.Account{Email: f.owner + "@example.test", Name: "Test Account", Country: "RU", StoreFront: "143469"}
}

func (f *fakeApple) OwnerId() string { return f.owner }

func (f *fakeApple) Versions(context.Context, int64) ([]string, string, error) {
	return nil, "", errors.New("synthetic versions unavailable")
}

func (f *fakeApple) VersionInfo(context.Context, int64, string, func(float64)) (applekit.VersionMetadata, error) {
	return applekit.VersionMetadata{}, errors.New("synthetic versions unavailable")
}

func (f *fakeApple) Profile(context.Context) (applekit.AccountProfile, error) {
	return applekit.AccountProfile{}, errors.New("synthetic profile unavailable")
}

func (f *fakeApple) SessionParameters() applekit.SessionParameters {
	return applekit.SessionParameters{Storefront: f.Account().StoreFront, Bag: applekit.ServiceBag{DownloadURL: "https://buy.itunes.apple.com/download"}}
}

func (f *fakeApple) SessionSecrets() (applekit.SessionSecrets, error) {
	return applekit.SessionSecrets{PasswordToken: "session-secret-marker"}, nil
}

func (f *fakeApple) catalog(ctx context.Context) ([]applekit.CatalogApp, error) {
	if f.owner == "offline" {
		return nil, fmt.Errorf("temporary network outage")
	}
	if f.owner == "expired" {
		return nil, applekit.ErrSessionExpired
	}
	if f.owner == "unicode" {
		return []applekit.CatalogApp{{Id: 1, Name: "Приложение Банка"}, {Id: 2, Name: "Ёлка"}}, nil
	}
	items := []applekit.CatalogApp{}
	count := 32
	if f.owner == "bulk" {
		count = 1_100
	}
	for i := 1; i <= count; i++ {
		var size *int64
		artworkUrl := ""
		if ((f.owner == "artwork" || f.owner == "progress" || f.owner == "cache-clear") && i == 1) || (f.owner == "history-artwork" && i == 2) {
			artworkUrl = "https://is1-ssl.mzstatic.com/test.png"
		}
		if f.owner == "cache-clear" && i == 2 {
			artworkUrl = "https://is1-ssl.mzstatic.com/second.png"
		}
		if i%2 != 0 {
			n := int64(i) * 1_000_000
			size = &n
		}
		items = append(items, applekit.CatalogApp{Id: int64(i), Name: fmt.Sprintf("App %02d", i), Delisted: i%2 == 0, Size: size, ArtworkUrl: artworkUrl, PurchasedAt: fmt.Sprintf("2026-01-%02dT12:00:00Z", (i-1)%28+1)})
	}
	return items, nil
}

func (f *fakeApple) Catalog(ctx context.Context, progress func(float64)) ([]applekit.CatalogApp, error) {
	progress(25)
	apps, err := f.catalog(ctx)
	if err == nil {
		progress(100)
	}
	return apps, err
}

func (f *fakeApple) writeIpa(ctx context.Context, id int64, path string, progress func(int64, int64)) (string, error) {
	if id == 32 && f.owner != "bulk" {
		<-ctx.Done()
		return "", ctx.Err()
	}
	file, err := os.Create(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	archive := zip.NewWriter(file)
	writer, err := archive.Create("Payload/Test.app/Info.plist")
	if err != nil {
		return "", err
	}
	data, err := plist.Marshal(map[string]string{"CFBundleIdentifier": fmt.Sprintf("test.app%d", id), "CFBundleVersion": "1.0", "CFBundleShortVersionString": "1.2.3", "MinimumOSVersion": "16.2", "CFBundleName": "Test"}, plist.XMLFormat)
	if err != nil {
		return "", err
	}
	if _, err = writer.Write(data); err != nil {
		return "", err
	}
	if f.owner == "ipa-artwork" {
		icon, err := archive.Create("iTunesArtwork")
		if err != nil {
			return "", err
		}
		icon.Write(testIcon())
	}
	if err = archive.Close(); err != nil {
		return "", err
	}
	progress(int64(len(data)), int64(len(data)))
	return path, nil
}

func (f *fakeApple) PrepareDownload(_ context.Context, id int64, versionId string) (applekit.Download, error) {
	return func(ctx context.Context, path string, progress func(applekit.DownloadProgress), _ applekit.DownloadOptions) (string, error) {
		return f.Download(ctx, id, versionId, path, progress)
	}, nil
}

func (f *fakeApple) Download(ctx context.Context, id int64, versionId, path string, progress func(applekit.DownloadProgress)) (string, error) {
	if (f.owner == "transfer" || f.owner == "preparing") && id == 32 {
		if err := os.WriteFile(path+".tmp", []byte("incomplete"), 0600); err != nil {
			return "", err
		}
		progress(applekit.DownloadProgress{Received: 1_000, Total: 10_000, Stage: "downloading"})
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
		progress(applekit.DownloadProgress{Received: 2_000, Total: 10_000, Stage: "downloading"})
		if f.owner == "preparing" {
			progress(applekit.DownloadProgress{Stage: "preparing"})
		}
		<-ctx.Done()
		return "", ctx.Err()
	}

	return f.writeIpa(ctx, id, path, func(n, total int64) {
		progress(applekit.DownloadProgress{Received: n, Total: total, Stage: "downloading"})
	})
}

type harness struct {
	artworkCalls *atomic.Int32
	app          *server.App
	http         *httptest.Server
	dist         *httptest.Server
	client       *http.Client
	csrf         string
}

func launch(t *testing.T, dir string, client *http.Client, artwork ...func(context.Context, string) ([]byte, error)) *harness {
	t.Helper()
	if err := os.MkdirAll(dir+"/packages", 0o700); err != nil {
		t.Fatal(err)
	}
	artworkCalls := &atomic.Int32{}
	cfg := server.Config{DataDir: dir, Artwork: func(ctx context.Context, raw string) ([]byte, error) {
		artworkCalls.Add(1)
		if len(artwork) > 0 {
			return artwork[0](ctx, raw)
		}
		return testIcon(), nil
	}, Apple: &testApple{signIn: func(ctx context.Context, email, password, code string) (account.Session, error) {
		if code == "" {
			return nil, applekit.ErrTwoFactorRequired
		}
		return &fakeApple{strings.Split(email, "@")[0]}, nil
	}, open: func(record db.Identity) (account.Session, error) {
		return &fakeApple{record.Owner}, nil
	}}}
	app, err := newTestServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if client == nil {
		jar, _ := cookiejar.New(nil)
		client = &http.Client{Jar: jar, Timeout: 5 * time.Second}
	}
	h := &harness{artworkCalls: artworkCalls, app: app, http: httptest.NewServer(app.Handler()), dist: httptest.NewServer(app.Distribution()), client: client}
	var state wire.State
	h.request(t, "GET", "/api/state", nil, 200, &state)
	h.csrf = state.Csrf
	return h
}

func (h *harness) close() { h.http.Close(); h.dist.Close(); h.app.Close() }

func (h *harness) request(t *testing.T, method, path string, input any, status int, output any) []byte {
	t.Helper()
	var body io.Reader
	if input != nil {
		data, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, h.http.URL+path, body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", h.csrf)
	resp, err := h.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != status {
		t.Fatalf("%s %s: got %d, want %d: %s", method, path, resp.StatusCode, status, data)
	}
	if bytes.Contains(data, []byte("session-secret-marker")) || bytes.Contains(data, []byte("password-marker")) {
		t.Fatal("secret leaked through API")
	}
	if output != nil {
		if err = json.Unmarshal(data, output); err != nil {
			t.Fatal(err)
		}
	}
	return data
}

func (h *harness) login(t *testing.T, owner string) {
	t.Helper()
	h.request(t, "POST", "/api/login", wire.LoginRequest{Email: owner + "@example.test", Password: "password-marker"}, 401, nil)
	h.request(t, "POST", "/api/login", wire.LoginRequest{Email: owner + "@example.test", Password: "password-marker", Code: "123456"}, 200, nil)
}

func (h *harness) refresh(t *testing.T) {
	t.Helper()
	h.request(t, "POST", "/api/catalog/refresh", nil, 202, nil)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var state wire.State
		h.request(t, "GET", "/api/state", nil, 200, &state)
		if state.Catalog.Status == "ready" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("catalog did not complete")
}

func (h *harness) waitDone(t *testing.T, n int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var page wire.LibraryPage
		h.request(t, "GET", "/api/library", nil, 200, &page)
		// The library lists other accounts' files too, so wait until nothing is still transferring.
		done, transferring := 0, false
		for _, job := range page.Items {
			switch job.Status {
			case "done":
				done++
			case "failed":
				t.Fatalf("download failed: %+v", job)
			default:
				transferring = true
			}
		}
		if done >= n && !transferring {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("downloads did not complete")
}

func TestCompleteFlowAndPersistence(t *testing.T) {
	dir := t.TempDir()
	h := launch(t, dir, nil)
	defer func() {
		if h != nil {
			h.close()
		}
	}()
	h.login(t, "first")
	h.refresh(t)
	var page wire.CatalogPage
	h.request(t, "GET", "/api/catalog?limit=10", nil, 200, &page)
	if len(page.Items) != 10 || !page.Next || page.Previous {
		t.Fatalf("bad first page: %+v", page)
	}
	first := page.Items[0].Id
	h.request(t, "GET", "/api/catalog?limit=10&cursor="+url.QueryEscape(page.Items[len(page.Items)-1].Cursor), nil, 200, &page)
	if page.Items[0].Id == first || !page.Previous {
		t.Fatal("paging did not advance")
	}
	h.request(t, "GET", "/api/catalog?limit=10&cursor="+url.QueryEscape(page.Items[0].Cursor)+"&move=prev", nil, 200, &page)
	if page.Items[0].Id != first {
		t.Fatal("previous page did not restore first row")
	}
	h.request(t, "GET", "/api/catalog?delisted=true", nil, 200, &page)
	for _, item := range page.Items {
		if !item.Delisted {
			t.Fatal("delisted filter leaked a listed app")
		}
	}
	var selected wire.SelectionResult
	h.request(t, "POST", "/api/catalog/selection", wire.Selection{Filter: &wire.Filter{Query: "App 0"}}, 200, &selected)
	if len(selected.Ids) != 9 {
		t.Fatalf("selection count %d", len(selected.Ids))
	}
	h.request(t, "PUT", "/api/preferences", wire.PreferencesInput{Language: "en", Theme: "light"}, 200, nil)
	h.request(t, "POST", "/api/downloads", wire.DownloadRequest{Selection: wire.Selection{Ids: []string{"1", "2", "3"}}}, 202, nil)
	h.waitDone(t, 3)
	var library wire.LibraryPage
	h.request(t, "GET", "/api/library", nil, 200, &library)
	if len(library.Items) != 3 {
		t.Fatalf("library: %+v", library)
	}
	for _, item := range library.Items {
		if item.Size == 0 {
			t.Fatalf("wrong ownership or size: %+v", item)
		}
	}
	var shared wire.Share
	h.request(t, "POST", "/api/shares", wire.Selection{Ids: []string{library.Items[0].Id, library.Items[1].Id}}, 201, &shared)
	if shared.Count != 2 {
		t.Fatal("wrong share count")
	}
	install, err := url.Parse(shared.Url)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := url.Parse(install.Query().Get("url"))
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Scheme != "https" || manifest.Port() != "8443" {
		t.Fatal(manifest)
	}
	resp, err := h.client.Get(h.dist.URL + manifest.Path)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var doc struct {
		Items []struct {
			Assets []struct {
				Url string `plist:"url"`
			} `plist:"assets"`
		} `plist:"items"`
	}
	if _, err = plist.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Items) != 2 {
		t.Fatal("manifest did not contain selected apps")
	}
	asset, _ := url.Parse(doc.Items[0].Assets[0].Url)
	if asset.Scheme != "http" || asset.Port() != "8080" {
		t.Fatal(asset)
	}
	req, _ := http.NewRequest("GET", h.http.URL+asset.Path, nil)
	req.Header.Set("Range", "bytes=0-9")
	resp, err = h.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	part, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 206 || len(part) != 10 {
		t.Fatal("package byte ranges unsupported")
	}
	h.request(t, "POST", "/api/shares", wire.Selection{Ids: []string{library.Items[0].Id, "00000000-0000-0000-0000-000000000000"}}, 404, nil)
	client := h.client
	h.close()
	h = nil
	h = launch(t, dir, client)
	var state wire.State
	h.request(t, "GET", "/api/state", nil, 200, &state)
	if state.Account == nil || state.Account.Email != "first@example.test" || state.Preferences.Language != "en" || state.Preferences.Theme != "light" {
		t.Fatalf("state not restored: %+v", state)
	}
	h.request(t, "GET", "/api/library", nil, 200, &library)
	if len(library.Items) != 3 {
		t.Fatal("library did not survive restart")
	}
	h.request(t, "POST", "/api/logout", nil, 204, nil)
	h.login(t, "second")
	h.refresh(t)
	h.request(t, "POST", "/api/downloads", wire.DownloadRequest{Selection: wire.Selection{Ids: []string{"4"}}}, 202, nil)
	h.waitDone(t, 1)
	h.request(t, "GET", "/api/library", nil, 200, &library)
	if len(library.Items) != 4 {
		t.Fatalf("shared library missing files: %+v", library)
	}
	h.request(t, "POST", "/api/shares", wire.Selection{Ids: []string{library.Items[0].Id}}, 201, &shared)
	h.request(t, "POST", "/api/library/delete", wire.Selection{Filter: &wire.Filter{}}, 409, nil)
	h.request(t, "POST", "/api/shares/stop", wire.Selection{Ids: []string{shared.Id}}, 204, nil)
	h.request(t, "POST", "/api/library/delete", wire.Selection{Filter: &wire.Filter{}}, 204, nil)
	h.request(t, "GET", "/api/library", nil, 200, &library)
	if len(library.Items) != 0 {
		t.Fatal("delete-all left library rows")
	}
	resp, err = h.client.Get(h.dist.URL + manifest.Path)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatal("stopped hosting still serves a manifest")
	}
	h.request(t, "POST", "/api/logout", nil, 204, nil)
	h.close()
	h = nil
	h = launch(t, dir, client)
	h.request(t, "GET", "/api/state", nil, 200, &state)
	if state.Account != nil {
		t.Fatal("logged-out session was restored")
	}
}

func TestValidationAndIsolation(t *testing.T) {
	h := launch(t, t.TempDir(), nil)
	defer h.close()
	h.request(t, "POST", "/api/login", map[string]any{"email": "first@example.test", "password": "password-marker", "unexpected": true}, 400, nil)
	for _, input := range []wire.LoginRequest{{}, {Email: "   ", Password: "x"}, {Email: "x@example.test", Password: "x", Code: "12AB56"}, {Email: "x@example.test", Password: strings.Repeat("x", 1_025)}} {
		h.request(t, "POST", "/api/login", input, 400, nil)
	}
	h.request(t, "GET", "/api/library", nil, 200, nil)
	h.login(t, "first")
	for _, path := range []string{"/api/library?limit=0", "/api/library?limit=9999999999", "/api/library?cursor=broken", "/api/library?unknown=1", "/api/library?q=" + url.QueryEscape(strings.Repeat("x", 201))} {
		h.request(t, "GET", path, nil, 400, nil)
	}
	h.request(t, "POST", "/api/downloads", wire.DownloadRequest{Selection: wire.Selection{Ids: []string{"1"}}}, 404, nil)
	h.request(t, "POST", "/api/shares", wire.Selection{Ids: []string{"../../data/session.json"}}, 400, nil)
	req, _ := http.NewRequest("POST", h.http.URL+"/api/logout", nil)
	resp, err := h.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatal("missing CSRF token accepted")
	}
	req, _ = http.NewRequest("GET", h.http.URL+"/api/state", nil)
	req.Host = "attacker.example"
	resp, err = h.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatal("foreign host accepted")
	}
	for _, path := range []string{"/data/metadata.db", "/data/library.db", "/api/state", "/packages/../../metadata.db", "/manifests/invalid.plist"} {
		resp, err = h.client.Get(h.dist.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 404 {
			t.Fatalf("distribution exposed %s", path)
		}
	}
	h.refresh(t)
	h.request(t, "POST", "/api/downloads", wire.DownloadRequest{Selection: wire.Selection{Ids: []string{"32"}}}, 202, nil)
	h.request(t, "POST", "/api/logout", nil, 204, nil)
	h.request(t, "GET", "/api/catalog", nil, 401, nil)
	h.login(t, "first")
	var jobs wire.LibraryPage
	h.request(t, "GET", "/api/library", nil, 200, &jobs)
	for _, job := range jobs.Items {
		if job.Status == "queued" || job.Status == "downloading" {
			t.Fatal("logout left active jobs")
		}
	}
}

func TestExpiredSessionAndTemporaryOutage(t *testing.T) {
	for _, owner := range []string{"offline", "expired"} {
		t.Run(owner, func(t *testing.T) {
			h := launch(t, t.TempDir(), nil)
			defer h.close()
			h.login(t, owner)
			h.request(t, "POST", "/api/catalog/refresh", nil, 202, nil)
			deadline := time.Now().Add(time.Second)
			for time.Now().Before(deadline) {
				var state wire.State
				h.request(t, "GET", "/api/state", nil, 200, &state)
				if owner == "expired" && state.Account == nil {
					if state.Catalog.Status != "failed" || state.Catalog.Error != "session_expired" {
						t.Fatal("missing session expiry reason")
					}
					return
				}
				if owner == "offline" && state.Catalog.Status == "failed" {
					if state.Account == nil || state.Catalog.Error != "apple_unavailable" {
						t.Fatalf("network outage discarded session: %+v", state)
					}
					return
				}
				time.Sleep(10 * time.Millisecond)
			}
			t.Fatal("unexpected session state")
		})
	}
}

func TestDataDirectoryLock(t *testing.T) {
	dir := t.TempDir()
	h := launch(t, dir, nil)
	defer h.close()
	if other, err := newTestServer(server.Config{DataDir: dir}); err == nil {
		other.Close()
		t.Fatal("second process accepted the same data directory")
	}
}

func TestDeleteAllExceedsSelectionLimit(t *testing.T) {
	dir := t.TempDir()
	h := launch(t, dir, nil)
	defer h.close()

	// Seed the deletion fixture directly; download behavior has its own test.
	conn, err := sql.Open("sqlite", filepath.Join(dir, "metadata.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	tx, err := conn.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.Exec("INSERT INTO accounts(id) VALUES('bulk')"); err != nil {
		t.Fatal(err)
	}
	insert, err := tx.Prepare("INSERT INTO packages(id,app_id,name,account_id,bundle,version,size,created_at) VALUES(?,?,?,?,?,?,?,?)")
	if err != nil {
		t.Fatal(err)
	}
	defer insert.Close()
	ids := make([]string, 1_001)
	for i := range ids {
		ids[i] = fmt.Sprintf("01900000-0000-7000-8000-%012d", i+1)
		if err := os.WriteFile(filepath.Join(dir, "packages", ids[i]+".ipa"), []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := insert.Exec(ids[i], i+1, "Fixture", "bulk", "test.app", "1", 7, "2026-10-25T10:00:00Z"); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	h.login(t, "bulk")
	var library wire.LibraryPage
	h.request(t, "GET", "/api/library", nil, 200, &library)
	if library.Total != int64(len(ids)) {
		t.Fatal("large library fixture is incomplete")
	}
	h.request(t, "POST", "/api/library/delete", wire.Selection{Ids: ids}, 400, nil)
	h.request(t, "POST", "/api/library/delete", wire.Selection{Filter: &wire.Filter{}}, 204, nil)
	h.request(t, "GET", "/api/library", nil, 200, &library)
	if library.Total != 0 || len(library.Items) != 0 {
		t.Fatal("delete-all was limited to a selection page")
	}
	files, err := os.ReadDir(filepath.Join(dir, "packages"))
	if err != nil || len(files) != 0 {
		t.Fatal("delete-all left package files", err)
	}
}

func TestDownloadSelectionLimit(t *testing.T) {
	dir := t.TempDir()
	h := launch(t, dir, nil)
	defer h.close()
	h.login(t, "bulk")
	h.refresh(t)
	var rejected wire.Error
	h.request(t, "POST", "/api/catalog/selection", wire.Selection{Filter: &wire.Filter{}}, 400, &rejected)
	if rejected.Code != "selection_too_large" {
		t.Fatal(rejected)
	}
	ids := make([]string, wire.MaxSelectionSize+1)
	for i := range ids {
		ids[i] = strconv.Itoa(i + 1)
	}
	h.request(t, "POST", "/api/downloads", wire.DownloadRequest{Selection: wire.Selection{Ids: ids}}, 400, nil)
	h.request(t, "POST", "/api/downloads", wire.DownloadRequest{Selection: wire.Selection{Ids: ids[:wire.MaxSelectionSize]}}, 202, nil)
	conn, err := sql.Open("sqlite", filepath.Join(dir, "metadata.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var active int
		if err := conn.QueryRow("SELECT count(*) FROM downloads WHERE status IN ('queued','downloading','preparing','canceling')").Scan(&active); err != nil {
			t.Fatal(err)
		}
		if active < wire.MaxSelectionSize {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("first batch did not release queue capacity")
		}
		time.Sleep(10 * time.Millisecond)
	}
	h.request(t, "POST", "/api/downloads", wire.DownloadRequest{Selection: wire.Selection{Ids: ids[wire.MaxSelectionSize:]}}, 202, nil)
	var library wire.LibraryPage
	h.request(t, "GET", "/api/library", nil, 200, &library)
	h.request(t, "POST", "/api/library/selection", wire.Selection{Filter: &wire.Filter{}}, 400, &rejected)
	if rejected.Code != "selection_too_large" {
		t.Fatal(rejected)
	}
	if library.Total != int64(wire.MaxSelectionSize+1) {
		t.Fatal("download selection limit lost accepted jobs")
	}
}

func TestUnicodeSearch(t *testing.T) {
	h := launch(t, t.TempDir(), nil)
	defer h.close()
	h.login(t, "unicode")
	h.refresh(t)
	var page wire.CatalogPage
	h.request(t, "GET", "/api/catalog?q="+url.QueryEscape("приложение"), nil, 200, &page)
	if len(page.Items) != 1 || page.Items[0].Id != "1" {
		t.Fatal("Unicode case folding failed")
	}
}

func TestCancelTransferAndPreparation(t *testing.T) {
	for _, owner := range []string{"transfer", "preparing"} {
		t.Run(owner, func(t *testing.T) {
			dir := t.TempDir()
			h := launch(t, dir, nil)
			defer h.close()
			h.login(t, owner)
			h.refresh(t)
			h.request(t, "POST", "/api/downloads", wire.DownloadRequest{Selection: wire.Selection{Ids: []string{"32"}}}, 202, nil)
			var item wire.LibraryItem
			deadline := time.Now().Add(2 * time.Second)
			for time.Now().Before(deadline) {
				var p wire.LibraryPage
				h.request(t, "GET", "/api/library", nil, 200, &p)
				if len(p.Items) == 1 {
					item = p.Items[0]
					if (owner == "transfer" && item.Received == 2_000 && item.Speed > 0) || (owner == "preparing" && item.Status == "preparing") {
						break
					}
				}
				time.Sleep(10 * time.Millisecond)
			}
			if item.Received != 2_000 || item.Total != 10_000 {
				t.Fatalf("lost progress: %+v", item)
			}
			if owner == "transfer" && item.Speed <= 0 {
				t.Fatal("no transfer speed")
			}
			if owner == "preparing" && (item.Status != "preparing" || item.Speed != 0) {
				t.Fatal("preparation reported as transfer")
			}
			h.request(t, "POST", "/api/downloads", wire.DownloadRequest{Selection: wire.Selection{Ids: []string{"31"}}}, 202, nil)
			var p wire.LibraryPage
			h.request(t, "GET", "/api/library", nil, 200, &p)
			for _, entry := range p.Items {
				if entry.AppId == "31" {
					h.request(t, "POST", "/api/downloads/"+entry.Id+"/cancel", nil, 202, nil)
				}
			}
			h.request(t, "POST", "/api/shares", wire.Selection{Ids: []string{item.Id}}, 409, nil)
			h.request(t, "POST", "/api/downloads/"+item.Id+"/cancel", nil, 202, nil)
			deadline = time.Now().Add(time.Second)
			for time.Now().Before(deadline) {
				h.request(t, "GET", "/api/library", nil, 200, &p)
				if len(p.Items) == 0 {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if len(p.Items) != 0 {
				t.Fatalf("canceled download stayed in library: %+v", p)
			}
			entries, err := os.ReadDir(dir + "/packages")
			if err != nil || len(entries) != 0 {
				t.Fatalf("cancel left partial files: %v, %v", entries, err)
			}
			h.request(t, "POST", "/api/downloads", wire.DownloadRequest{Selection: wire.Selection{Ids: []string{"1"}}}, 202, nil)
			h.waitDone(t, 1)
		})
	}
}

func TestSortedCursorPages(t *testing.T) {
	h := launch(t, t.TempDir(), nil)
	defer h.close()
	h.login(t, "first")
	h.refresh(t)
	var page wire.CatalogPage
	h.request(t, "GET", "/api/catalog?limit=10", nil, 200, &page)
	if page.Items[0].PurchasedAt != "2026-01-28T12:00:00Z" || page.Items[9].PurchasedAt != "2026-01-19T12:00:00Z" {
		t.Fatal("default is not newest purchases first")
	}
	h.request(t, "GET", "/api/catalog?limit=10&sort=status&cursor="+url.QueryEscape(page.Items[len(page.Items)-1].Cursor), nil, 400, nil)
	for _, sort := range []string{"name", "status"} {
		for _, direction := range []string{"asc", "desc"} {
			base := "/api/catalog?limit=10&sort=" + sort + "&direction=" + direction
			cursor := ""
			seen := map[string]bool{}
			first := ""
			previousStatus := direction == "desc"
			var previousName string
			for {
				h.request(t, "GET", base+cursor, nil, 200, &page)
				if first == "" {
					first = page.Items[0].Id
				}
				for _, item := range page.Items {
					if seen[item.Id] {
						t.Fatal("cursor repeated a row")
					}
					seen[item.Id] = true
					if sort == "status" {
						if (direction == "asc" && previousStatus && !item.Delisted) || (direction == "desc" && !previousStatus && item.Delisted) {
							t.Fatal("status sorting failed")
						}
						previousStatus = item.Delisted
					} else {
						if previousName != "" && ((direction == "asc" && previousName > item.Name) || (direction == "desc" && previousName < item.Name)) {
							t.Fatal("name sorting failed")
						}
						previousName = item.Name
					}
				}
				if !page.Next {
					break
				}
				cursor = "&cursor=" + url.QueryEscape(page.Items[len(page.Items)-1].Cursor)
			}
			if len(seen) != 32 {
				t.Fatalf("missing rows: %d", len(seen))
			}
			for page.Previous {
				h.request(t, "GET", base+"&cursor="+url.QueryEscape(page.Items[0].Cursor)+"&move=prev", nil, 200, &page)
			}
			if page.Items[0].Id != first {
				t.Fatal("reverse pagination changed first row")
			}
		}
	}
	for _, q := range []string{"sort=unknown", "sort=name%3BDROP%20TABLE%20files", "direction=sideways"} {
		h.request(t, "GET", "/api/catalog?"+q, nil, 400, nil)
	}
}

func TestForwardedHost(t *testing.T) {
	dir := t.TempDir()
	os.Mkdir(dir+"/packages", 0700)
	app, err := newTestServer(server.Config{DataDir: dir, Hosts: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	for _, host := range []string{"192.168.50.10:8080", "10.10.0.1:8080", "172.16.2.3:8080", "169.254.1.2:8080", "[fd00::1]:8080", "127.0.0.1:8080", "8.8.8.8:8080", "attacker.example:8080"} {
		request := httptest.NewRequest("GET", "http://"+host+"/api/health", nil)
		response := httptest.NewRecorder()
		app.Handler().ServeHTTP(response, request)
		want := 200
		if strings.HasPrefix(host, "attacker") || strings.HasPrefix(host, "8.8.8.8") {
			want = 400
		}
		if response.Code != want {
			t.Fatalf("host %s: %d", host, response.Code)
		}
	}
	request := httptest.NewRequest("GET", "http://192.168.50.10:8000/packages/missing/missing.ipa", nil)
	response := httptest.NewRecorder()
	app.Distribution().ServeHTTP(response, request)
	if response.Code != 404 {
		t.Fatal(response.Code)
	}
}

func TestCatalogLibraryMarker(t *testing.T) {
	dir := t.TempDir()
	h := launch(t, dir, nil)
	defer h.close()
	h.login(t, "first")
	h.refresh(t)

	check := func(want bool) {
		t.Helper()
		var page wire.CatalogPage
		h.request(t, "GET", "/api/catalog?q=App%2001", nil, 200, &page)
		if len(page.Items) != 1 || page.Items[0].InLibrary != want {
			t.Fatalf("wrong library marker: %+v", page.Items)
		}
	}

	check(false)
	h.request(t, "POST", "/api/downloads", wire.DownloadRequest{Selection: wire.Selection{Ids: []string{"1"}}}, 202, nil)
	h.waitDone(t, 1)
	check(true)
	h.request(t, "POST", "/api/logout", nil, 204, nil)
	h.login(t, "second")
	h.refresh(t)
	check(true)
	h.request(t, "POST", "/api/logout", nil, 204, nil)
	h.login(t, "first")
	check(true)
	h.request(t, "POST", "/api/downloads", wire.DownloadRequest{Selection: wire.Selection{Ids: []string{"1"}}}, 202, nil)
	requireAppCopies(t, dir, 1, 1, 1)
	check(true)
	var library wire.LibraryPage
	h.request(t, "GET", "/api/library", nil, 200, &library)
	h.request(t, "POST", "/api/library/delete", wire.Selection{Ids: []string{library.Items[0].Id}}, 204, nil)
	check(false)
	h.request(t, "POST", "/api/library/delete", wire.Selection{Filter: &wire.Filter{}}, 204, nil)
	check(false)
}

func TestLibrarySelectionReadiness(t *testing.T) {
	h := launch(t, t.TempDir(), nil)
	defer h.close()
	h.login(t, "transfer")
	h.refresh(t)
	h.request(t, "POST", "/api/downloads", wire.DownloadRequest{Selection: wire.Selection{Ids: []string{"1"}}}, 202, nil)
	h.waitDone(t, 1)
	h.request(t, "POST", "/api/downloads", wire.DownloadRequest{Selection: wire.Selection{Ids: []string{"32"}}}, 202, nil)
	var selected wire.LibrarySelectionResult
	h.request(t, "POST", "/api/library/selection", wire.Selection{Filter: &wire.Filter{}}, 200, &selected)
	if len(selected.Ids) != 2 || len(selected.ReadyIds) != 1 {
		t.Fatalf("incorrect selection readiness: %+v", selected)
	}
	h.request(t, "POST", "/api/shares", wire.Selection{Ids: selected.ReadyIds}, 201, nil)
	h.request(t, "POST", "/api/shares", wire.Selection{Ids: selected.Ids}, 409, nil)
	h.request(t, "POST", "/api/library/selection", wire.Selection{Ids: selected.ReadyIds}, 200, &selected)
	if len(selected.Ids) != 1 || len(selected.ReadyIds) != 1 {
		t.Fatal("explicit selection includes another file")
	}
	h.request(t, "POST", "/api/library/selection", wire.Selection{Ids: selected.Ids, Filter: &wire.Filter{Query: "ambiguous"}}, 400, nil)
}

func TestCatalogSizePaging(t *testing.T) {
	h := launch(t, t.TempDir(), nil)
	defer h.close()
	h.login(t, "first")
	h.refresh(t)
	for _, direction := range []string{"asc", "desc"} {
		var page wire.CatalogPage
		cursor := ""
		count := 0
		seen := map[string]bool{}
		last := int64(-1)
		if direction == "desc" {
			last = 4_611_686_018_427_387_904
		}
		for {
			h.request(t, "GET", "/api/catalog?limit=10&sort=size&direction="+direction+cursor, nil, 200, &page)
			for _, app := range page.Items {
				n := int64(-1)
				if app.Size != nil {
					n = *app.Size
				}
				if (direction == "asc" && n < last) || (direction == "desc" && n > last) {
					t.Fatal("size sorting failed")
				}
				if seen[app.Id] {
					t.Fatal("row repeated")
				}
				seen[app.Id] = true
				last = n
				count++
			}
			if !page.Next {
				break
			}
			cursor = "&cursor=" + url.QueryEscape(page.Items[len(page.Items)-1].Cursor)
		}
		if count != 32 {
			t.Fatal("size paging lost rows")
		}
	}
}

func testIcon() []byte {
	var b bytes.Buffer
	img := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	img.SetNRGBA(0, 0, color.NRGBA{R: 20, G: 120, B: 240, A: 255})
	png.Encode(&b, img)
	return b.Bytes()
}

func TestCachedArtworkAccess(t *testing.T) {
	h := launch(t, t.TempDir(), nil)
	defer h.close()
	h.login(t, "artwork")
	h.refresh(t)
	var page wire.CatalogPage
	h.request(t, "GET", "/api/catalog?q=App%2001", nil, 200, &page)
	if len(page.Items) != 1 || page.Items[0].Icon == "" {
		t.Fatal("no cached icon")
	}
	path := page.Items[0].Icon

	check := func(status int) {
		t.Helper()
		body := h.request(t, "GET", path, nil, status, nil)
		if status == 200 {
			if _, err := png.Decode(bytes.NewReader(body)); err != nil {
				t.Fatal("invalid icon response")
			}
		}
	}

	check(200)
	h.refresh(t)
	if h.artworkCalls.Load() != 1 {
		t.Fatal("unchanged artwork downloaded twice")
	}
	h.request(t, "POST", "/api/downloads", wire.DownloadRequest{Selection: wire.Selection{Ids: []string{"1"}}}, 202, nil)
	h.waitDone(t, 1)
	var library wire.LibraryPage
	h.request(t, "GET", "/api/library", nil, 200, &library)
	if library.Items[0].Icon != path {
		t.Fatal("library uses different icon")
	}
	h.request(t, "POST", "/api/logout", nil, 204, nil)
	check(200)
	h.login(t, "second")
	check(200)
}

func TestArtworkFromDownloadedIpa(t *testing.T) {
	dir := t.TempDir()
	h := launch(t, dir, nil)
	h.login(t, "ipa-artwork")
	h.refresh(t)
	h.request(t, "POST", "/api/downloads", wire.DownloadRequest{Selection: wire.Selection{Ids: []string{"1"}}}, 202, nil)
	h.waitDone(t, 1)
	var library wire.LibraryPage
	h.request(t, "GET", "/api/library", nil, 200, &library)
	if library.Items[0].Icon == "" || h.artworkCalls.Load() != 0 {
		h.close()
		t.Fatal("IPA artwork not used offline")
	}
	path := library.Items[0].Icon
	h.close()
	h = launch(t, dir, nil)
	defer h.close()
	h.request(t, "GET", path, nil, 200, nil)
}

func TestDelistedArtworkWithoutDownloadedIpa(t *testing.T) {
	h := launch(t, t.TempDir(), nil)
	defer h.close()
	h.login(t, "history-artwork")
	h.refresh(t)
	var page wire.CatalogPage
	h.request(t, "GET", "/api/catalog?delisted=true&q=App%2002", nil, 200, &page)
	if len(page.Items) != 1 || !page.Items[0].Delisted || page.Items[0].Icon == "" {
		t.Fatal("delisted purchase artwork missing")
	}
	path := page.Items[0].Icon
	body := h.request(t, "GET", path, nil, 200, nil)
	if _, err := png.Decode(bytes.NewReader(body)); err != nil {
		t.Fatal("delisted artwork is not a valid image")
	}
	var library wire.LibraryPage
	h.request(t, "GET", "/api/library", nil, 200, &library)
	if len(library.Items) != 0 {
		t.Fatal("artwork required an IPA download")
	}
	h.refresh(t)
	if h.artworkCalls.Load() != 1 {
		t.Fatal("unchanged delisted artwork was fetched again")
	}
}

func TestSharedTableSettingsPersistAndValidate(t *testing.T) {
	dir := t.TempDir()
	h := launch(t, dir, nil)
	defer func() {
		if h != nil {
			h.close()
		}
	}()
	input := wire.CatalogView{Sort: "size", Direction: "desc", Delisted: false}
	var saved wire.Saved[wire.CatalogView]
	h.request(t, "PUT", "/api/table-settings/catalog", input, 200, &saved)
	if saved.Value != input || saved.Revision < 2 {
		t.Fatal("table settings not saved")
	}
	// A new browser has no device cookie from the first client.
	other := &http.Client{Timeout: 5 * time.Second}

	read := func() wire.State {
		t.Helper()
		response, err := other.Get(h.http.URL + "/api/state")
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var state wire.State
		if err = json.NewDecoder(response.Body).Decode(&state); err != nil {
			t.Fatal(err)
		}
		return state
	}

	state := read()
	if state.Tables.Catalog != saved {
		t.Fatal("another device did not receive table settings")
	}
	if state.Tables.Library.Value.Sort != "createdAt" || state.Tables.Library.Value.Direction != "desc" {
		t.Fatal("catalog preferences changed library")
	}
	previous := saved.Revision
	state = read()
	if state.Tables.Catalog.Value.Sort != "size" || state.Tables.Catalog.Revision != previous {
		t.Fatal("reading settings changed shared sorting")
	}
	h.close()
	h = nil
	h = launch(t, dir, nil)
	state = read()
	if state.Tables.Catalog.Value.Sort != "size" || state.Tables.Catalog.Value.Delisted {
		t.Fatal("table settings did not survive restart")
	}
	for _, invalid := range []wire.CatalogView{
		{Sort: "version", Direction: "asc"},
		{Sort: "name", Direction: "wrong"},
		{Sort: "", Direction: "asc"},
	} {
		h.request(t, "PUT", "/api/table-settings/catalog", invalid, 400, nil)
	}
	h.request(t, "PUT", "/api/table-settings/library", map[string]any{"sort": "name", "direction": "asc", "delisted": true}, 400, nil)
	h.request(t, "PUT", "/api/table-settings/profile", input, 404, nil)
	h.request(t, "PUT", "/api/table-settings/catalog", map[string]any{"limit": 50, "sort": "name", "direction": "asc", "delisted": true}, 400, nil)
	h.request(t, "PUT", "/api/table-settings/catalog", map[string]any{"sort": "name", "direction": "asc", "delisted": true, "query": "temporary"}, 400, nil)
	h.request(t, "PUT", "/api/table-settings/catalog", map[string]any{"sort": "name", "direction": "asc", "delisted": true, "query": "", "extra": true}, 400, nil)
}

func TestCatalogProgressIncludesArtworkAndCompletion(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var released sync.Once
	h := launch(t, t.TempDir(), nil, func(ctx context.Context, _ string) ([]byte, error) {
		close(started)
		select {
		case <-release:
			return testIcon(), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
	defer h.close()
	defer released.Do(func() { close(release) })
	h.login(t, "progress")
	h.request(t, "POST", "/api/catalog/refresh", nil, 202, nil)
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("artwork was not requested")
	}
	var state wire.State
	h.request(t, "GET", "/api/state", nil, 200, &state)
	if state.Catalog.Status != "loading" || state.Catalog.Progress < 80 || state.Catalog.Progress >= 100 {
		t.Fatalf("missing intermediate progress: %s %v", state.Catalog.Status, state.Catalog.Progress)
	}
	released.Do(func() { close(release) })
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		h.request(t, "GET", "/api/state", nil, 200, &state)
		if state.Catalog.Status == "ready" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if state.Catalog.Status != "ready" || state.Catalog.Progress != 100 {
		t.Fatal("catalog did not finish at 100 percent")
	}
	h.request(t, "POST", "/api/logout", nil, 204, nil)
	h.request(t, "GET", "/api/state", nil, 200, &state)
	if state.Catalog.Progress != 0 {
		t.Fatal("sign-out retained previous progress")
	}
}

func TestMultipleSharesStopAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	h := launch(t, dir, nil)
	defer func() {
		if h != nil {
			h.close()
		}
	}()
	h.login(t, "single-share")
	h.refresh(t)
	h.request(t, "POST", "/api/downloads", wire.DownloadRequest{Selection: wire.Selection{Ids: []string{"1", "2"}}}, 202, nil)
	h.waitDone(t, 2)
	var library wire.LibraryPage
	h.request(t, "GET", "/api/library", nil, 200, &library)
	input := wire.Selection{Ids: []string{library.Items[0].Id, library.Items[1].Id}}
	type result struct {
		share wire.Share
		err   error
	}
	results := make(chan result, 2)
	for i := range 2 {
		data, _ := json.Marshal(wire.Selection{Ids: input.Ids[:i+1]})
		go func() {
			req, _ := http.NewRequest("POST", h.http.URL+"/api/shares", bytes.NewReader(data))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-CSRF-Token", h.csrf)
			response, err := h.client.Do(req)
			if err != nil {
				results <- result{err: err}
				return
			}
			defer response.Body.Close()
			var share wire.Share
			if response.StatusCode != 201 {
				err = fmt.Errorf("unexpected share status %d", response.StatusCode)
			} else {
				err = json.NewDecoder(response.Body).Decode(&share)
			}
			results <- result{share, err}
		}()
	}
	ids := map[string]int{}
	for range 2 {
		r := <-results
		if r.err != nil {
			t.Fatal(r.err)
		}
		ids[r.share.Id] = r.share.Count
	}
	if len(ids) != 2 {
		t.Fatal("concurrent share IDs collide")
	}
	h.close()
	h = nil
	h = launch(t, dir, nil)
	var state wire.State
	h.request(t, "GET", "/api/state", nil, 200, &state)
	if state.ShareCount != 0 {
		t.Fatal("restart restored shares")
	}
	for id := range ids {
		// Stopping a share that already ended is not an error.
		h.request(t, "POST", "/api/shares/stop", wire.Selection{Ids: []string{id}}, 204, nil)
		h.request(t, "GET", "/packages/"+id+"/"+input.Ids[0]+".ipa", nil, 404, nil)
		response, err := h.client.Get(h.dist.URL + "/manifests/" + id + ".plist")
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != 404 {
			t.Fatal("stopped manifest still available")
		}
	}

	var shared wire.Share
	h.request(t, "POST", "/api/shares", input, 201, &shared)
	h.request(t, "POST", "/api/library/delete", wire.Selection{Filter: &wire.Filter{}}, 409, nil)
	h.request(t, "POST", "/api/shares/stop", wire.Selection{Ids: []string{shared.Id}}, 204, nil)
	h.request(t, "POST", "/api/library/delete", wire.Selection{Filter: &wire.Filter{}}, 204, nil)
	h.request(t, "GET", "/api/state", nil, 200, &state)
	if state.ShareCount > 0 {
		t.Fatal("empty share retained after deleting last file")
	}
}

func TestCustomPageSizes(t *testing.T) {
	dir := t.TempDir()
	h := launch(t, dir, nil)
	defer func() {
		if h != nil {
			h.close()
		}
	}()
	h.login(t, "pager")
	h.refresh(t)
	for _, limit := range []int{1, 20, 25, 31, 37, 100, wire.MaxPageSize} {
		var page wire.CatalogPage
		h.request(t, "GET", fmt.Sprintf("/api/catalog?delisted=true&limit=%d", limit), nil, 200, &page)
		want := min(limit, 16)
		if len(page.Items) != want {
			t.Fatalf("limit %d returned %d rows", limit, len(page.Items))
		}
		if page.Next {
			var next wire.CatalogPage
			h.request(t, "GET", fmt.Sprintf("/api/catalog?delisted=true&limit=%d&cursor=%s", limit, url.QueryEscape(page.Items[len(page.Items)-1].Cursor)), nil, 200, &next)
			if len(next.Items) == 0 || len(next.Items) > limit || !next.Previous {
				t.Fatal("custom-size cursor navigation failed")
			}
			var previous wire.CatalogPage
			h.request(t, "GET", fmt.Sprintf("/api/catalog?delisted=true&limit=%d&cursor=%s", limit, url.QueryEscape(next.Items[0].Cursor)+"&move=prev"), nil, 200, &previous)
			if len(previous.Items) != len(page.Items) || previous.Items[0].Id != page.Items[0].Id {
				t.Fatal("previous page did not restore rows")
			}
		}
		h.request(t, "GET", fmt.Sprintf("/api/library?limit=%d", limit), nil, 200, nil)
	}
	for _, limit := range []int{-1, 0, wire.MaxPageSize + 1} {
		h.request(t, "GET", fmt.Sprintf("/api/catalog?limit=%d", limit), nil, 400, nil)
	}
}

func TestArtworkQueueUsesCatalogDeadline(t *testing.T) {
	var fullBudget atomic.Bool
	h := launch(t, t.TempDir(), nil, func(ctx context.Context, _ string) ([]byte, error) {
		deadline, ok := ctx.Deadline()
		if ok && time.Until(deadline) > 10*time.Minute {
			fullBudget.Store(true)
			return testIcon(), nil
		}
		return nil, fmt.Errorf("artwork queue deadline cuts off remaining images")
	})
	defer h.close()
	h.login(t, "history-artwork")
	h.refresh(t)
	var page wire.CatalogPage
	h.request(t, "GET", "/api/catalog?delisted=true&q=App%2002", nil, 200, &page)
	if !fullBudget.Load() || len(page.Items) != 1 || page.Items[0].Icon == "" {
		t.Fatal("artwork queue lost catalog budget or icon")
	}
}

func TestClearCatalogCachePreservesLibraryAndOtherAccount(t *testing.T) {
	dir := t.TempDir()
	h := launch(t, dir, nil, func(_ context.Context, raw string) ([]byte, error) {
		if !strings.Contains(raw, "second.png") {
			return testIcon(), nil
		}
		var data bytes.Buffer
		img := image.NewNRGBA(image.Rect(0, 0, 1, 1))
		img.SetNRGBA(0, 0, color.NRGBA{R: 42, G: 200, B: 17, A: 255})
		err := png.Encode(&data, img)
		return data.Bytes(), err
	})
	defer func() {
		if h != nil {
			h.close()
		}
	}()
	h.request(t, "DELETE", "/api/catalog", nil, 401, nil)
	h.login(t, "history-artwork")
	h.refresh(t)
	var other wire.CatalogPage
	h.request(t, "GET", "/api/catalog?delisted=true&q=App%2002", nil, 200, &other)
	otherIcon := other.Items[0].Icon
	h.request(t, "POST", "/api/logout", nil, 204, nil)
	h.login(t, "cache-clear")
	h.refresh(t)
	var page wire.CatalogPage
	h.request(t, "GET", "/api/catalog?limit=37&sort=name&direction=asc", nil, 200, &page)
	if len(page.Items) != 32 || page.Items[0].Icon == "" || page.Items[1].Icon == "" {
		t.Fatal("cache fixture incomplete")
	}
	unusedIcon := page.Items[1].Icon
	h.request(t, "POST", "/api/downloads", wire.DownloadRequest{Selection: wire.Selection{Ids: []string{"1"}}}, 202, nil)
	h.waitDone(t, 1)
	var library wire.LibraryPage
	h.request(t, "GET", "/api/library", nil, 200, &library)
	var share wire.Share
	h.request(t, "POST", "/api/shares", wire.Selection{Ids: []string{library.Items[0].Id}}, 201, &share)
	h.request(t, "PUT", "/api/table-settings/catalog", wire.CatalogView{Sort: "size", Direction: "desc", Delisted: true}, 200, nil)
	beforeCalls := h.artworkCalls.Load()
	h.request(t, "DELETE", "/api/catalog", nil, 204, nil)
	h.request(t, "GET", "/api/catalog", nil, 200, &page)
	if len(page.Items) != 0 || page.Next || page.Previous {
		t.Fatal("cached catalog remains")
	}
	var state wire.State
	h.request(t, "GET", "/api/state", nil, 200, &state)
	if state.Account == nil || state.Catalog.Cached || state.Catalog.Status != "idle" || state.Catalog.Progress != 0 || state.ShareCount == 0 || h.shares(t)[0].Id != share.Id || state.Tables.Catalog.Value.Sort != "size" || !state.Tables.Catalog.Value.Delisted {
		t.Fatal("clearing changed account, share, settings or left catalog state")
	}
	h.request(t, "GET", "/api/library", nil, 200, &library)
	if len(library.Items) != 1 || library.Items[0].Icon == "" {
		t.Fatal("clearing damaged library")
	}
	h.request(t, "GET", library.Items[0].Icon, nil, 200, nil)
	h.request(t, "GET", unusedIcon, nil, 404, nil)
	if _, err := os.Stat(filepath.Join(dir, "icons", strings.TrimPrefix(unusedIcon, "/api/icons/"))); !os.IsNotExist(err) {
		t.Fatal("unused artwork file remains")
	}
	if h.artworkCalls.Load() != beforeCalls {
		t.Fatal("clearing fetched artwork")
	}
	h.request(t, "DELETE", "/api/catalog", nil, 204, nil)
	h.request(t, "POST", "/api/logout", nil, 204, nil)
	h.login(t, "history-artwork")
	h.request(t, "GET", "/api/catalog?delisted=true&q=App%2002", nil, 200, &other)
	if len(other.Items) != 1 || other.Items[0].Icon != otherIcon {
		t.Fatal("another account cache was cleared")
	}
	h.request(t, "GET", otherIcon, nil, 200, nil)
	h.request(t, "POST", "/api/logout", nil, 204, nil)
	h.login(t, "cache-clear")
	h.close()
	h = nil
	h = launch(t, dir, nil)
	h.request(t, "GET", "/api/catalog", nil, 200, &page)
	if len(page.Items) != 0 {
		t.Fatal("cleared catalog restored after restart")
	}
	h.refresh(t)
	h.request(t, "GET", "/api/catalog", nil, 200, &page)
	if len(page.Items) == 0 {
		t.Fatal("catalog cannot be refreshed after clearing")
	}
}

func TestClearCatalogCacheRejectsConcurrentRefresh(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	h := launch(t, t.TempDir(), nil, func(ctx context.Context, _ string) ([]byte, error) {
		close(started)
		select {
		case <-release:
			return testIcon(), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
	defer h.close()
	defer once.Do(func() { close(release) })
	h.login(t, "history-artwork")
	h.request(t, "POST", "/api/catalog/refresh", nil, 202, nil)
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("refresh did not start")
	}
	h.request(t, "DELETE", "/api/catalog", nil, 409, nil)
	once.Do(func() { close(release) })
}

func TestExportSelectedIpaAndZip(t *testing.T) {
	dir := t.TempDir()
	h := launch(t, dir, nil)
	defer h.close()
	h.request(t, "POST", "/api/library/export", wire.Selection{Ids: []string{"01900000-0000-7000-8000-000000000001"}}, 404, nil)
	h.login(t, "export")
	h.refresh(t)
	h.request(t, "POST", "/api/downloads", wire.DownloadRequest{Selection: wire.Selection{Ids: []string{"1", "2"}}}, 202, nil)
	h.waitDone(t, 2)
	var library wire.LibraryPage
	h.request(t, "GET", "/api/library", nil, 200, &library)
	ids := []string{library.Items[0].Id, library.Items[1].Id}
	original := map[string][]byte{}
	for _, item := range library.Items {
		body, err := os.ReadFile(filepath.Join(dir, "packages", item.Id+".ipa"))
		if err != nil {
			t.Fatal(err)
		}
		original[item.AppId] = body
	}
	var one wire.FileExport
	h.request(t, "POST", "/api/library/export", wire.Selection{Ids: ids[:1]}, 201, &one)
	response, err := h.client.Get(h.http.URL + one.Url)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	disposition, params, err := mime.ParseMediaType(response.Header.Get("Content-Disposition"))
	if response.StatusCode != 200 || err != nil || disposition != "attachment" || !strings.HasSuffix(params["filename"], ".ipa") || !bytes.Equal(body, original[library.Items[0].AppId]) {
		t.Fatal("single IPA export changed package or attachment name")
	}
	req, _ := http.NewRequest("GET", h.http.URL+one.Url, nil)
	req.Header.Set("Range", "bytes=0-7")
	response, err = h.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	partial, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 206 || !bytes.Equal(partial, body[:8]) {
		t.Fatal("IPA range download failed")
	}
	var many wire.FileExport
	h.request(t, "POST", "/api/library/export", wire.Selection{Ids: ids}, 201, &many)
	response, err = h.client.Get(h.http.URL + many.Url)
	if err != nil {
		t.Fatal(err)
	}
	archiveBody, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 200 || response.Header.Get("Content-Type") != "application/zip" {
		t.Fatal("multiple IPA export is not ZIP")
	}
	archive, err := zip.NewReader(bytes.NewReader(archiveBody), int64(len(archiveBody)))
	if err != nil {
		t.Fatal(err)
	}
	if len(archive.File) != 2 {
		t.Fatal("ZIP file count differs from selection")
	}
	for _, file := range archive.File {
		if file.Method != zip.Store || strings.ContainsAny(file.Name, `/\`) || !strings.HasSuffix(file.Name, ".ipa") {
			t.Fatal("unsafe or recompressed ZIP entry")
		}
		reader, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(reader)
		reader.Close()
		matched := false
		for _, expected := range original {
			matched = matched || bytes.Equal(expected, data)
		}
		if !matched {
			t.Fatal("ZIP entry differs from original IPA")
		}
	}
	var state wire.State
	h.request(t, "GET", "/api/state", nil, 200, &state)
	if state.ShareCount > 0 {
		t.Fatal("file export started OTA sharing")
	}
	h.request(t, "POST", "/api/library/export", wire.Selection{}, 400, nil)
	h.request(t, "POST", "/api/library/export", wire.Selection{Ids: []string{ids[0], ids[0]}}, 400, nil)
	h.request(t, "POST", "/api/library/export", wire.Selection{Ids: []string{"../../secret"}}, 400, nil)
	h.request(t, "POST", "/api/logout", nil, 204, nil)
	h.request(t, "GET", one.Url, nil, 200, nil)
	h.login(t, "another-export")
	h.request(t, "GET", one.Url, nil, 200, nil)
	var other wire.FileExport
	h.request(t, "POST", "/api/library/export", wire.Selection{Ids: ids[:1]}, 201, &other)
	body = h.request(t, "GET", other.Url, nil, 200, nil)
	if !bytes.Equal(body, original[library.Items[0].AppId]) {
		t.Fatal("export from another account changed IPA bytes")
	}
	h.request(t, "POST", "/api/logout", nil, 204, nil)
	h.login(t, "export")
	h.request(t, "POST", "/api/library/delete", wire.Selection{Ids: ids[:1]}, 204, nil)
	h.request(t, "GET", one.Url, nil, 404, nil)
}

func TestExportRejectsUnfinishedApps(t *testing.T) {
	h := launch(t, t.TempDir(), nil)
	defer h.close()
	h.login(t, "transfer")
	h.refresh(t)
	h.request(t, "POST", "/api/downloads", wire.DownloadRequest{Selection: wire.Selection{Ids: []string{"32"}}}, 202, nil)
	var jobs wire.LibraryPage
	h.request(t, "GET", "/api/library", nil, 200, &jobs)
	if len(jobs.Items) != 1 {
		t.Fatal("missing job")
	}
	h.request(t, "POST", "/api/library/export", wire.Selection{Ids: []string{jobs.Items[0].Id}}, 409, nil)
}

func TestDeleteSharedSelectionKeepsWholeGroup(t *testing.T) {
	dir := t.TempDir()
	h := launch(t, dir, nil)
	defer h.close()
	h.login(t, "shared-delete")
	h.refresh(t)
	h.request(t, "POST", "/api/downloads", wire.DownloadRequest{Selection: wire.Selection{Ids: []string{"1", "2", "3"}}}, 202, nil)
	h.waitDone(t, 3)
	var library wire.LibraryPage
	h.request(t, "GET", "/api/library", nil, 200, &library)
	var shared wire.Share
	h.request(t, "POST", "/api/shares", wire.Selection{Ids: []string{library.Items[1].Id}}, 201, &shared)
	original := make(map[string][]byte)
	for _, item := range library.Items {
		data, err := os.ReadFile(filepath.Join(dir, "packages", item.Id+".ipa"))
		if err != nil {
			t.Fatal(err)
		}
		original[item.Id] = data
	}
	for _, selected := range []wire.Selection{{Ids: []string{library.Items[0].Id, library.Items[1].Id, library.Items[2].Id}}, {Filter: &wire.Filter{}}} {
		var failure wire.Error
		h.request(t, "POST", "/api/library/delete", selected, 409, &failure)
		if failure.Code != "shared_files" {
			t.Fatalf("unexpected rejection: %s", failure.Code)
		}
		var remaining wire.LibraryPage
		h.request(t, "GET", "/api/library", nil, 200, &remaining)
		if len(remaining.Items) != 3 {
			t.Fatal("rejected deletion changed the group")
		}
		for id, data := range original {
			kept, err := os.ReadFile(filepath.Join(dir, "packages", id+".ipa"))
			if err != nil || !bytes.Equal(kept, data) {
				t.Fatal("rejected deletion changed an IPA")
			}
		}
		var state wire.State
		h.request(t, "GET", "/api/state", nil, 200, &state)
		if state.ShareCount == 0 || h.shares(t)[0].Id != shared.Id || h.shares(t)[0].Count != 1 {
			t.Fatal("rejected deletion changed sharing")
		}
	}
	// Unrelated files remain deletable while sharing is active.
	h.request(t, "POST", "/api/library/delete", wire.Selection{Ids: []string{library.Items[0].Id}}, 204, nil)
	h.request(t, "POST", "/api/shares/stop", wire.Selection{Ids: []string{shared.Id}}, 204, nil)
	h.request(t, "POST", "/api/library/delete", wire.Selection{Filter: &wire.Filter{}}, 204, nil)
	h.request(t, "GET", "/api/library", nil, 200, &library)
	if len(library.Items) != 0 {
		t.Fatal("files cannot be deleted after stopping sharing")
	}
}

func TestDelistedSettingsNormalizeStatusSorting(t *testing.T) {
	h := launch(t, t.TempDir(), nil)
	defer h.close()
	var saved wire.Saved[wire.CatalogView]
	input := wire.CatalogView{Sort: "status", Direction: "desc", Delisted: false}
	h.request(t, "PUT", "/api/table-settings/catalog", input, 200, &saved)
	if saved.Value != input {
		t.Fatal("status sorting disabled for a mixed list")
	}
	input.Delisted = true
	h.request(t, "PUT", "/api/table-settings/catalog", input, 200, &saved)
	if saved.Value.Sort != "name" || saved.Value.Direction != "asc" || !saved.Value.Delisted {
		t.Fatal("single-status list retained status sorting")
	}
	var state wire.State
	h.request(t, "GET", "/api/state", nil, 200, &state)
	if state.Tables.Catalog != saved {
		t.Fatal("normalized sort was not saved")
	}
	library := wire.LibraryView{Sort: "status", Direction: "desc"}
	var savedLibrary wire.Saved[wire.LibraryView]
	h.request(t, "PUT", "/api/table-settings/library", library, 200, &savedLibrary)
	if savedLibrary.Value != library {
		t.Fatal("download status sorting changed")
	}
}

func TestSharedPreferencesPersistAcrossDevices(t *testing.T) {
	dir := t.TempDir()
	h := launch(t, dir, nil)
	defer func() {
		if h != nil {
			h.close()
		}
	}()
	other := &http.Client{Timeout: 5 * time.Second}

	read := func() wire.State {
		t.Helper()
		response, err := other.Get(h.http.URL + "/api/state")
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var result wire.State
		if err = json.NewDecoder(response.Body).Decode(&result); err != nil {
			t.Fatal(err)
		}
		for _, cookie := range response.Cookies() {
			if cookie.Name == "ipa_device" {
				t.Fatal("preferences still create a device cookie")
			}
		}
		return result
	}

	initial := read().Preferences
	if initial.Language != "en" || initial.Theme != "dark" || initial.Revision != 1 {
		t.Fatal("unexpected defaults")
	}
	var saved wire.Preferences
	h.request(t, "PUT", "/api/preferences", wire.PreferencesInput{Language: "ru", Theme: "light"}, 200, &saved)
	if saved.Revision <= initial.Revision || saved.Language != "ru" || saved.Theme != "light" {
		t.Fatal("settings were not saved")
	}
	if read().Preferences != saved {
		t.Fatal("another device did not receive preferences")
	}
	h.request(t, "PUT", "/api/preferences", wire.PreferencesInput{Language: "en", Theme: "dark"}, 200, &saved)
	if saved.Revision <= initial.Revision+1 || read().Preferences != saved {
		t.Fatal("updated preferences did not propagate")
	}
	h.request(t, "PUT", "/api/preferences", wire.PreferencesInput{Language: "wrong", Theme: "dark"}, 400, nil)
	if read().Preferences != saved {
		t.Fatal("invalid request changed preferences")
	}
	h.close()
	h = nil
	h = launch(t, dir, nil)
	if read().Preferences != saved {
		t.Fatal("shared preferences did not survive restart")
	}
}

func TestLibraryAcrossAccounts(t *testing.T) {
	dir := t.TempDir()
	h := launch(t, dir, nil)
	defer func() {
		if h != nil {
			h.close()
		}
	}()
	h.login(t, "first")
	h.refresh(t)
	h.request(t, "POST", "/api/downloads", wire.DownloadRequest{Selection: wire.Selection{Ids: []string{"1"}}}, 202, nil)
	h.waitDone(t, 1)
	var library wire.LibraryPage
	h.request(t, "GET", "/api/library", nil, 200, &library)
	foreign := library.Items[0].Id
	h.request(t, "POST", "/api/logout", nil, 204, nil)
	h.login(t, "second")
	h.refresh(t)
	h.request(t, "POST", "/api/downloads", wire.DownloadRequest{Selection: wire.Selection{Ids: []string{"2"}}}, 202, nil)
	h.waitDone(t, 1)
	h.request(t, "GET", "/api/library", nil, 200, &library)
	if len(library.Items) != 2 {
		t.Fatal("all-account library missing ready files")
	}
	var selection wire.LibrarySelectionResult
	h.request(t, "POST", "/api/library/selection", wire.Selection{Filter: &wire.Filter{}}, 200, &selection)
	if len(selection.ReadyIds) != 2 {
		t.Fatal("all-account selection failed", selection)
	}
	var shared wire.Share
	h.request(t, "POST", "/api/shares", wire.Selection{Ids: []string{foreign}}, 201, &shared)
	var state wire.State
	h.request(t, "GET", "/api/state", nil, 200, &state)
	if state.ShareCount == 0 || h.shares(t)[0].Id != shared.Id {
		t.Fatal("foreign share cannot be restored")
	}
	response, err := h.client.Get(h.dist.URL + "/manifests/" + shared.Id + ".plist")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal("foreign manifest unavailable")
	}
	h.request(t, "POST", "/api/library/delete", wire.Selection{Filter: &wire.Filter{}}, 409, nil)
	h.close()
	h = nil
	h = launch(t, dir, nil)
	h.request(t, "GET", "/api/library", nil, 200, &library)
	if len(library.Items) != 2 {
		t.Fatal("shared library did not survive restart")
	}
	h.request(t, "GET", "/api/state", nil, 200, &state)
	if state.ShareCount != 0 {
		t.Fatal("shared hosting survived restart")
	}
	h.request(t, "POST", "/api/shares/stop", wire.Selection{Ids: []string{shared.Id}}, 204, nil)
	h.request(t, "POST", "/api/library/delete", wire.Selection{Ids: []string{foreign}}, 204, nil)
	h.request(t, "GET", "/api/library", nil, 200, &library)
	if len(library.Items) != 1 || library.Items[0].Id == foreign {
		t.Fatal("another account's file was not deleted")
	}
	if _, err := os.Stat(filepath.Join(dir, "packages", foreign+".ipa")); !os.IsNotExist(err) {
		t.Fatal("deleted IPA remains on disk")
	}
}

func TestLoginAcceptsPhoneIdentifier(t *testing.T) {
	h := launch(t, t.TempDir(), nil)
	defer h.close()
	identifier := "+79990000000"
	h.request(t, "POST", "/api/login", wire.LoginRequest{Email: identifier, Password: "password-marker"}, 401, nil)
	var account wire.Account
	h.request(t, "POST", "/api/login", wire.LoginRequest{Email: " +7\u00a0(999)\u202f000\u201100\u201100 ", Password: "password-marker", Code: "123456"}, 200, &account)
	if account.Id != strings.TrimPrefix(identifier, "+") {
		t.Fatal("phone identifier was not passed to the sign-in client")
	}
}

func TestCatalogPurchaseDatesAndFilteredTotals(t *testing.T) {
	dir := t.TempDir()
	h := launch(t, dir, nil)
	defer func() {
		if h != nil {
			h.close()
		}
	}()
	h.login(t, "metadata")
	h.refresh(t)
	h.request(t, "PUT", "/api/table-settings/catalog", wire.CatalogView{Sort: "purchasedAt", Direction: "desc"}, 200, nil)
	var page wire.CatalogPage
	h.request(t, "GET", "/api/catalog?limit=2&sort=purchasedAt&direction=desc", nil, 200, &page)
	if page.Total != 32 || len(page.Items) != 2 || page.Items[0].PurchasedAt != "2026-01-28T12:00:00Z" || !page.Next {
		t.Fatalf("purchase dates, sorting or total missing: %+v", page)
	}
	h.request(t, "GET", "/api/catalog?limit=2&sort=purchasedAt&direction=desc&cursor="+url.QueryEscape(page.Items[len(page.Items)-1].Cursor), nil, 200, &page)
	if page.Total != 32 || page.Items[0].PurchasedAt != "2026-01-26T12:00:00Z" {
		t.Fatal("purchase-date cursor or total changed across pages")
	}
	h.request(t, "GET", "/api/catalog?delisted=true&q=App%200&limit=2", nil, 200, &page)
	if page.Total != 4 || len(page.Items) != 2 {
		t.Fatalf("catalog total ignores filters: %+v", page)
	}
	h.request(t, "POST", "/api/downloads", wire.DownloadRequest{Selection: wire.Selection{Ids: []string{"1", "2"}}}, 202, nil)
	h.waitDone(t, 2)
	var library wire.LibraryPage
	h.request(t, "GET", "/api/library?limit=1", nil, 200, &library)
	if library.Total != 2 || len(library.Items) != 1 {
		t.Fatal("library total counts only the current page")
	}
	h.request(t, "GET", "/api/library?q=App%2001", nil, 200, &library)
	if library.Total != 1 {
		t.Fatal("library total ignores search")
	}
	h.close()
	h = nil
	h = launch(t, dir, nil)
	h.request(t, "GET", "/api/catalog?sort=purchasedAt&direction=desc&limit=2", nil, 200, &page)
	if page.Total != 32 || page.Items[0].PurchasedAt != "2026-01-28T12:00:00Z" {
		t.Fatal("purchase date did not survive restart")
	}
	h.request(t, "POST", "/api/logout", nil, 204, nil)
	h.login(t, "metadata-second")
	h.request(t, "GET", "/api/catalog", nil, 200, &page)
	if page.Total != 0 {
		t.Fatal("catalog total includes another account")
	}
	h.request(t, "GET", "/api/library", nil, 200, &library)
	if library.Total != 2 {
		t.Fatal("library total excludes shared ready files")
	}
}

func (h *harness) shares(t *testing.T) []wire.Share {
	t.Helper()
	var page wire.SharePage
	h.request(t, "GET", "/api/shares?limit=128", nil, 200, &page)
	return page.Items
}
