package storekit

import (
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/hyperion-cs/appleseum/applekit/internal/ams"
	"github.com/hyperion-cs/appleseum/applekit/internal/httpx"
)

func NewSessionHttpClient() *http.Client {
	return newSessionClient(newJar())
}

func NewSessionHttpClientWithTransport(transport http.RoundTripper) *http.Client {
	if transport == nil {
		return NewSessionHttpClient()
	}
	return httpx.NoRedirect(&http.Client{Transport: transport, Jar: newJar(), Timeout: 90 * time.Second})
}

func newSessionClient(jar *persistentJar) *http.Client {
	return httpx.NoRedirect(httpx.NewClient(jar))
}

// Keep the same HTTP client and cookie jar across native auth and store operations.
func SessionFromNative(client *http.Client, credentials ams.NativeCredentials, native ams.NativeSession, endpoints *ams.Bag) (*Session, error) {
	if client == nil || client.Jar == nil {
		return nil, errors.New("native session requires a cookie-bearing client")
	}
	if _, ok := client.Jar.(*persistentJar); !ok {
		return nil, errors.New("native session requires NewSessionHTTPClient")
	}
	if credentials.DirectoryServicesId == "" || native.DirectoryServicesId != credentials.DirectoryServicesId {
		return nil, errors.New("native session identity mismatch")
	}
	if native.PasswordToken == "" || native.Storefront == "" {
		return nil, errors.New("native session is incomplete")
	}
	hardwareId, err := hex.DecodeString(credentials.Guid)
	if err != nil || len(hardwareId) != 6 {
		return nil, errors.New("native session has an invalid GUID")
	}
	if endpoints == nil || !httpx.IsAppleUrl(endpoints.DownloadURL) {
		return nil, errors.New("native session has an invalid download endpoint")
	}
	now := time.Now().UTC()
	return &Session{
		Account: Account{
			Email: credentials.AppleId, PasswordToken: native.PasswordToken,
			DirectoryServicesId: native.DirectoryServicesId, StoreFront: native.Storefront,
			Pod: native.Pod, Guid: strings.ToUpper(credentials.Guid),
		},
		Client:          client,
		hardware:        hardwareId,
		amd:             ams.AmdController{Bag: *endpoints},
		authenticatedAt: now,
		lastSuccessAt:   now,
	}, nil
}
