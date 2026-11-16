// Package applekit authenticates App Store accounts and downloads licensed IPAs.
// Subpackages handle IPA inspection, artwork and OTA manifests.
package applekit

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/hyperion-cs/appleseum/applekit/internal/httpx"
	"github.com/hyperion-cs/appleseum/applekit/internal/storekit"
)

const (
	DefaultUserAgent = httpx.DefaultConfiguratorUserAgent

	// Deprecated: use DefaultUserAgent.
	UserAgent = DefaultUserAgent
)

// ErrSessionExpired identifies an explicit authentication rejection from Apple.
var ErrSessionExpired = storekit.ErrSessionExpired

type SessionRejection = storekit.SessionRejection

type LoginError = storekit.LoginError

// ErrTwoFactorRequired means Apple accepted the code request.
// Resume the same authenticator with the verification code.
var ErrTwoFactorRequired = errors.New("two-factor verification code required")

type App struct {
	Id   int64 // App Store numeric id (adam id)
	Name string
	Kind string // media kind, e.g. "iOS App"
}

type Account struct {
	Email      string
	Name       string
	StoreFront string
	Country    string
}

type Client struct{ s *storekit.Session }

func SignIn(ctx context.Context, appleId, password string) (*Client, error) {
	return SignInWithCode(ctx, appleId, password, "")
}

func SignInWithCode(ctx context.Context, appleId, password, code string) (*Client, error) {
	auth, err := defaultAuthenticator()
	if err != nil {
		return nil, err
	}
	return auth.SignIn(ctx, appleId, password, code)
}

func (c *Client) Account() Account {
	a := c.s.Account
	return Account{Email: a.Email, Name: a.Name, StoreFront: a.StoreFront, Country: storekit.CountryFromStorefront(a.StoreFront)}
}

// Apps returns every iOS app the account owns.
func (c *Client) Apps(ctx context.Context) ([]App, error) {
	ps, err := c.s.ListPurchases(ctx)
	if err != nil {
		return nil, err
	}
	return toApps(ps), nil
}

// DelistedApps returns owned iOS apps missing from the account country's storefront.
func (c *Client) DelistedApps(ctx context.Context) ([]App, error) {
	ps, err := c.s.DelistedIosApps(ctx)
	if err != nil {
		return nil, err
	}
	return toApps(ps), nil
}

// Versions returns entitled external version Ids, oldest first, and the latest ID.
func (c *Client) Versions(ctx context.Context, appId int64) (ids []string, latest string, err error) {
	return c.s.ListVersions(ctx, appId)
}

// Download writes an installable IPA. An empty versionId selects the latest version.
// A nil progress callback disables reporting.
func (c *Client) Download(ctx context.Context, appId int64, versionId, outPath string, progress func(DownloadProgress)) (string, error) {
	return c.s.Download(ctx, appId, versionId, outPath, progress)
}

func toApps(ps []storekit.Purchase) []App {
	out := make([]App, len(ps))
	for i, p := range ps {
		out[i] = App{Id: p.Id, Name: p.Name, Kind: p.Kind}
	}
	return out
}

// OwnerId returns the stable Apple account identifier for local package ownership.
func (c *Client) OwnerId() string { return c.s.Account.DirectoryServicesId }

type CatalogApp = storekit.CatalogApp

type AccountProfile = storekit.AccountProfile

// Profile reads account settings and counters without payment details.
func (c *Client) Profile(ctx context.Context) (AccountProfile, error) {
	return c.s.AccountProfile(ctx)
}

// Catalog lists purchases once and marks apps absent from the account storefront.
// A nil progress callback disables reporting.
func (c *Client) Catalog(ctx context.Context, progress func(float64)) ([]CatalogApp, error) {
	return c.s.Catalog(ctx, progress)
}

type DownloadProgress = storekit.DownloadProgress
type Download = storekit.Download
type DownloadOptions = storekit.DownloadOptions

var ErrDownloadTooLarge = storekit.ErrDownloadTooLarge

// PrepareDownload obtains a license. The returned transfer may run independently
// of this client's lifetime and concurrently with other prepared transfers.
func (c *Client) PrepareDownload(ctx context.Context, appId int64, versionId string) (Download, error) {
	return c.s.PrepareDownload(ctx, appId, versionId)
}

// NewHttpClient uses Apple TLS profiles; a nil jar creates a private cookie jar.
func NewHttpClient(jar http.CookieJar) *http.Client { return httpx.NewClient(jar) }

type RequestEvent = httpx.RequestEvent

func WithRequestObserver(ctx context.Context, observe func(RequestEvent)) context.Context {
	return httpx.WithRequestObserver(ctx, observe)
}

// WithRequestMetadataObserver reports requests without retaining response bodies.
func WithRequestMetadataObserver(ctx context.Context, observe func(RequestEvent)) context.Context {
	return httpx.WithRequestMetadataObserver(ctx, observe)
}

type ExchangeCapture = httpx.ExchangeCapture

func WithExchangeCapture(ctx context.Context, create func() *ExchangeCapture) context.Context {
	return httpx.WithExchangeCapture(ctx, create)
}

// SessionTimes reports observed successes, not the session's expiry time.
func (c *Client) SessionTimes() (time.Time, time.Time) { return c.s.SessionTimes() }

type VersionMetadata = storekit.VersionMetadata

// VersionInfo fetches the ZIP ranges needed for the main app's version and minimum OS.
// Progress reports completion from 0 to 100; nil disables reporting.
func (c *Client) VersionInfo(ctx context.Context, appId int64, versionId string, progress func(float64)) (VersionMetadata, error) {
	return c.s.VersionInfo(ctx, appId, versionId, progress)
}
