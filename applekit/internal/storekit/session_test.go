package storekit

import (
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hyperion-cs/appleseum/applekit/internal/ams"
	"github.com/hyperion-cs/appleseum/applekit/internal/httpx"
)

func TestSessionRoundTripCookies(t *testing.T) {
	jar := newJar()
	u, _ := url.Parse("https://buy.itunes.apple.com/auth/login")
	jar.SetCookies(u, []*http.Cookie{{Name: "session", Value: "preserved", HttpOnly: true}, {Name: "deleted", Value: "old", Domain: ".itunes.apple.com", Path: "/"}})
	jar.SetCookies(u, []*http.Cookie{{Name: "deleted", MaxAge: -1, Domain: "itunes.apple.com", Path: "/"}, {Name: "max-age", Value: "alive", Path: "/", MaxAge: 3_600, Expires: time.Now().Add(-time.Hour)}})
	session := &Session{Account: Account{Email: "example@example.test", DirectoryServicesId: "123", PasswordToken: "token", Guid: "001122334455"}, Client: httpx.NewClient(jar), amd: ams.AmdController{Bag: ams.Bag{DownloadURL: "https://buy.itunes.apple.com/download"}}}
	data, err := session.Export()
	if err != nil {
		t.Fatal(err)
	}
	restored, err := Restore(data)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]string{}
	for _, cookie := range restored.Client.Jar.Cookies(u) {
		found[cookie.Name] = cookie.Value
	}
	if found["session"] != "preserved" || found["max-age"] != "alive" || found["deleted"] != "" {
		t.Fatal(found)
	}
	if restored.Account.Guid != session.Account.Guid || restored.Account.PasswordToken != "token" {
		t.Fatal("identity not restored")
	}
}

func TestSessionPreservesCookieCreationOrder(t *testing.T) {
	u, _ := url.Parse("https://buy.example.test/login")
	jar := newJar()
	jar.SetCookies(u, []*http.Cookie{{Name: "token", Value: "parent", Domain: "example.test", Path: "/"}})
	jar.SetCookies(u, []*http.Cookie{{Name: "token", Value: "host", Path: "/"}})
	jar.SetCookies(u, []*http.Cookie{{Name: "token", Value: "updated-parent", Domain: "example.test", Path: "/"}})

	header := func(jar http.CookieJar) string {
		parts := []string{}
		for _, cookie := range jar.Cookies(u) {
			parts = append(parts, cookie.String())
		}
		return strings.Join(parts, "; ")
	}

	original := header(jar)
	session := &Session{Account: Account{DirectoryServicesId: "123", PasswordToken: "token", Guid: "001122334455"}, Client: httpx.NewClient(jar), amd: ams.AmdController{Bag: ams.Bag{DownloadURL: "https://buy.example.test/download"}}}
	data, err := session.Export()
	if err != nil {
		t.Fatal(err)
	}
	var stored sessionData
	if err = json.Unmarshal(data, &stored); err != nil {
		t.Fatal(err)
	}
	slices.Reverse(stored.Cookies)
	data, _ = json.Marshal(stored)
	restored, err := Restore(data)
	if err != nil {
		t.Fatal(err)
	}
	if header(restored.Client.Jar) != original {
		t.Fatal("cookie precedence changed after restore")
	}
	for i := 0; i < 5; i++ {
		data, _ = restored.Export()
		restored, err = Restore(data)
		if err != nil || header(restored.Client.Jar) != original {
			t.Fatal("cookie precedence changed after repeated restore")
		}
	}
}

func TestRestoredSessionKeepsRedownloadAndRotatedCookie(t *testing.T) {
	u, _ := url.Parse("https://downloaddispatch.itunes.apple.com/r/redownload")
	jar := newJar()
	jar.SetCookies(u, []*http.Cookie{{Name: "store-auth", Value: "initial", Domain: ".apple.com", Path: "/", MaxAge: 3_600, Secure: true}})
	session := &Session{
		Account: Account{DirectoryServicesId: "123", PasswordToken: "token", Guid: "001122334455"},
		Client:  httpx.NewClient(jar),
		amd:     ams.AmdController{Bag: ams.Bag{DownloadURL: "https://downloaddispatch.itunes.apple.com/WebObjects/DownloadDispatch.woa/wa/ent/download", RedownloadURL: u.String()}},
	}

	roundTrip := func(s *Session) *Session {
		t.Helper()
		data, err := s.Export()
		if err != nil {
			t.Fatal(err)
		}
		restored, err := Restore(data)
		if err != nil {
			t.Fatal(err)
		}
		return restored
	}

	restored := roundTrip(session)
	restored.Client.Jar.SetCookies(u, []*http.Cookie{{Name: "store-auth", Value: "rotated", Domain: ".apple.com", Path: "/", MaxAge: 7_200, Secure: true}})
	rotated := roundTrip(restored)
	if rotated.amd.Bag.RedownloadURL != u.String() {
		t.Fatal("modern redownload endpoint lost on restore")
	}
	cookies := rotated.Client.Jar.Cookies(u)
	if len(cookies) != 1 || cookies[0].Value != "rotated" {
		t.Fatal("updated store cookie lost on restore")
	}
	data, err := rotated.Export()
	if err != nil {
		t.Fatal(err)
	}
	var before sessionData
	if err = json.Unmarshal(data, &before); err != nil {
		t.Fatal(err)
	}
	data, err = roundTrip(rotated).Export()
	if err != nil {
		t.Fatal(err)
	}
	var after sessionData
	if err = json.Unmarshal(data, &after); err != nil {
		t.Fatal(err)
	}
	if len(before.Cookies) != 1 || len(after.Cookies) != 1 || !before.Cookies[0].Cookie.Expires.Equal(after.Cookies[0].Cookie.Expires) || after.Cookies[0].Cookie.MaxAge != 0 {
		t.Fatal("restore extended the server cookie lifetime")
	}
}

func TestRestoreLegacySessionWithoutRedownload(t *testing.T) {
	data := []byte(`{"Version":1,"Account":{"DirectoryServicesID":"123","PasswordToken":"token","GUID":"001122334455"},"DownloadURL":"https://buy.itunes.apple.com/download"}`)
	session, err := Restore(data)
	if err != nil || session.amd.Bag.RedownloadURL != "" {
		t.Fatal("legacy session did not restore")
	}
}

func TestRestoreRejectsInvalidRedownload(t *testing.T) {
	for _, endpoint := range []string{"http://downloaddispatch.itunes.apple.com/r/redownload", "https://example.test/redownload", "https://user@downloaddispatch.itunes.apple.com/r/redownload"} {
		data, err := json.Marshal(sessionData{Version: 1, Account: Account{DirectoryServicesId: "123", PasswordToken: "token", Guid: "001122334455"}, DownloadUrl: "https://buy.itunes.apple.com/download", RedownloadUrl: endpoint})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = Restore(data); err == nil {
			t.Fatal("accepted invalid redownload endpoint")
		}
	}
}

func TestSessionExportPrunesExpiredCookies(t *testing.T) {
	jar := newJar()
	u, _ := url.Parse("https://buy.example.test/")
	jar.SetCookies(u, []*http.Cookie{{Name: "session", Value: "live", Path: "/"}})
	jar.cookies["expired"] = savedCookie{URL: u.String(), Cookie: http.Cookie{Name: "old", Value: "expired", Expires: time.Now().Add(-time.Hour)}}
	s := &Session{Client: &http.Client{Jar: jar}}
	data, err := s.Export()
	if err != nil {
		t.Fatal(err)
	}
	var saved sessionData
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	if len(jar.cookies) != 1 || len(saved.Cookies) != 1 || saved.Cookies[0].Cookie.Name != "session" {
		t.Fatal("export must prune expired metadata and preserve session cookies")
	}
}
