package storekit

import (
	"cmp"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/publicsuffix"

	"github.com/hyperion-cs/appleseum/applekit/internal/ams"
	"github.com/hyperion-cs/appleseum/applekit/internal/httpx"
)

type Account struct {
	Email               string
	Name                string
	PasswordToken       string
	DirectoryServicesId string `json:"DirectoryServicesID"` // dsPersonId
	StoreFront          string
	Pod                 string
	Guid                string `json:"GUID"`
}

type Session struct {
	metaMu          sync.Mutex
	authenticatedAt time.Time
	lastSuccessAt   time.Time
	clock           func() time.Time
	Account         Account
	Client          *http.Client
	hardware        []byte
	amd             ams.AmdController
	metadata        ams.RequestContext
	kbsyncCache     cachedKbSync
}

func asString(v any) string { s, _ := v.(string); return s }

type savedCookie struct {
	Created uint64
	URL     string
	Cookie  http.Cookie
}
type persistentJar struct {
	http.CookieJar
	mu       sync.Mutex
	cookies  map[string]savedCookie
	sequence uint64
}

func newJar() *persistentJar {
	jar, _ := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
	return &persistentJar{CookieJar: jar, cookies: make(map[string]savedCookie)}
}

func (j *persistentJar) SetCookies(u *url.URL, cookies []*http.Cookie) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.CookieJar.SetCookies(u, cookies)
	now := time.Now()
	accepted := make(map[string]bool)
	for _, c := range cookies {
		valid, known := accepted[c.Domain]
		if !known {
			valid = acceptsCookieDomain(u, c)
			accepted[c.Domain] = valid
		}
		if !valid {
			continue
		}
		copy := *c
		if copy.Path == "" || copy.Path[0] != '/' {
			copy.Path = "/"
			if end := strings.LastIndex(u.Path, "/"); end > 0 {
				copy.Path = u.Path[:end]
			}
		}
		domain := strings.TrimPrefix(strings.ToLower(copy.Domain), ".")
		if domain == "" {
			domain = strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
		}
		key := domain + "\n" + copy.Path + "\n" + copy.Name
		if copy.MaxAge < 0 || (copy.MaxAge == 0 && !copy.Expires.IsZero() && !copy.Expires.After(now)) {
			delete(j.cookies, key)
			continue
		}
		if copy.MaxAge > 0 {
			copy.Expires = now.Add(time.Duration(copy.MaxAge) * time.Second)
			copy.MaxAge = 0
		}
		created := uint64(0)
		if previous, ok := j.cookies[key]; ok && (previous.Cookie.Expires.IsZero() || previous.Cookie.Expires.After(now)) {
			created = previous.Created
		}
		if created == 0 {
			j.sequence++
			created = j.sequence
		}
		j.cookies[key] = savedCookie{URL: u.String(), Cookie: copy, Created: created}
	}
}

// Use net/http's jar for domain/public-suffix/IP/IDNA validation.
// Ignore expiry here so deletion cookies are checked too.
func acceptsCookieDomain(u *url.URL, cookie *http.Cookie) bool {
	probe := *cookie
	probe.Path, probe.Secure, probe.MaxAge, probe.Expires = "/", false, 0, time.Time{}
	validator, _ := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
	validator.SetCookies(u, []*http.Cookie{&probe})
	target := *u
	target.Path = "/"
	return len(validator.Cookies(&target)) != 0
}

// Preserve cookie creation order when replaying the jar.
func restoreJar(saved []savedCookie) (*persistentJar, error) {
	jar := newJar()
	cookies := slices.Clone(saved)
	slices.SortStableFunc(cookies, func(a, b savedCookie) int { return cmp.Compare(a.Created, b.Created) })
	for _, c := range cookies {
		u, err := url.Parse(c.URL)
		if err != nil || u.Host == "" {
			return nil, errors.New("invalid saved cookie URL")
		}
		jar.SetCookies(u, []*http.Cookie{&c.Cookie})
	}
	return jar, nil
}

type sessionData struct {
	AuthenticatedAt time.Time
	LastSuccessAt   time.Time
	Version         int
	Account         Account
	Cookies         []savedCookie
	DownloadUrl     string                  `json:"DownloadURL"`
	RedownloadUrl   string                  `json:"RedownloadURL,omitempty"`
	Configurator    *savedConfiguratorState `json:",omitempty"`
}

func ReadLegacy(data []byte) (*Session, error) {
	var state sessionData
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	if state.Version != 1 || state.Account.PasswordToken == "" || state.Account.DirectoryServicesId == "" {
		return nil, fmt.Errorf("invalid saved session")
	}
	hardware, err := hex.DecodeString(state.Account.Guid)
	if err != nil || len(hardware) != 6 {
		return nil, fmt.Errorf("invalid saved hardware identifier")
	}
	u, err := url.Parse(state.DownloadUrl)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return nil, fmt.Errorf("invalid saved download endpoint")
	}
	if state.RedownloadUrl != "" && !httpx.IsAppleUrl(state.RedownloadUrl) {
		return nil, fmt.Errorf("invalid saved redownload endpoint")
	}
	jar, err := restoreJar(state.Cookies)
	if err != nil {
		return nil, err
	}
	session := &Session{Account: state.Account, Client: newSessionClient(jar), authenticatedAt: state.AuthenticatedAt, lastSuccessAt: state.LastSuccessAt, hardware: hardware, amd: ams.AmdController{Bag: ams.Bag{DownloadURL: state.DownloadUrl, RedownloadURL: state.RedownloadUrl}}}
	if err := session.restoreConfigurator(state.Configurator); err != nil {
		return nil, err
	}
	return session, nil
}
