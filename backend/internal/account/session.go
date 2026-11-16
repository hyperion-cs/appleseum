// Package account holds what an Apple session is made of: the session itself,
// its sealed secrets, sign-in failures, device settings and the cached profile.
package account

import (
	"context"
	"net/http"
	"time"

	"github.com/hyperion-cs/appleseum/applekit"

	"appleseum/backend/internal/db"
)

// Session is a signed-in Apple session; *applekit.Client implements it.
type Session interface {
	Account() applekit.Account
	OwnerId() string
	SessionParameters() applekit.SessionParameters
	SessionSecrets() (applekit.SessionSecrets, error)
	Catalog(ctx context.Context, progress func(float64)) ([]applekit.CatalogApp, error)
	PrepareDownload(ctx context.Context, appId int64, versionId string) (applekit.Download, error)
	Versions(ctx context.Context, appId int64) ([]string, string, error)
	VersionInfo(ctx context.Context, appId int64, versionId string, progress func(float64)) (applekit.VersionMetadata, error)
	Profile(ctx context.Context) (applekit.AccountProfile, error)
}

// Apple signs in on a device and reopens saved sessions; tests substitute synthetic sessions.
type Apple interface {
	SignIn(ctx context.Context, device *applekit.Authenticator, login, password, code string) (Session, error)
	Open(device *applekit.Authenticator, identity db.Identity, secrets applekit.SessionSecrets, transport *http.Client) (Session, error)
}

// AppleKit talks to Apple.
type AppleKit struct{}

func (AppleKit) SignIn(ctx context.Context, device *applekit.Authenticator, login, password, code string) (Session, error) {
	client, err := device.SignIn(ctx, login, password, code)
	if client == nil {
		return nil, err
	}
	return client, err
}

func (AppleKit) Open(device *applekit.Authenticator, identity db.Identity, secrets applekit.SessionSecrets, transport *http.Client) (Session, error) {
	client, err := device.OpenSession(identity.Account, identity.Owner, identity.Parameters, secrets, transport)
	if client == nil {
		return nil, err
	}
	return client, err
}

// Register saves what a live session changed in its device's registration.
func Register(device *applekit.Authenticator, session Session) error {
	if client, ok := session.(*applekit.Client); ok {
		return device.SaveRegistration(client)
	}
	return nil
}

// Release wipes a live session's secrets and returns its HTTP client, whose
// connections the next session on this device can reuse.
func Release(session Session) *http.Client {
	if client, ok := session.(*applekit.Client); ok {
		client.ForgetSecrets()
		return client.HttpClient()
	}
	return nil
}

func IdentityOf(session Session) db.Identity {
	return db.Identity{Owner: session.OwnerId(), Account: session.Account(), Parameters: session.SessionParameters()}
}

// SignedInAt is when Apple last accepted the password, or "" when unknown.
func SignedInAt(identity db.Identity) string {
	if identity.Parameters.AuthenticatedAt.IsZero() {
		return ""
	}
	return identity.Parameters.AuthenticatedAt.UTC().Format(time.RFC3339)
}

// Call runs one Apple operation of a workspace, signing in again once if Apple asks.
type Call func(ctx context.Context, action func(Session) error) error
