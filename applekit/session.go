package applekit

import (
	"errors"
	"net/http"

	"github.com/hyperion-cs/appleseum/applekit/internal/ams"
	"github.com/hyperion-cs/appleseum/applekit/internal/storekit"
)

type SessionCookie = storekit.SessionCookie
type SessionSecrets = storekit.SessionSecrets
type SessionParameters = storekit.SessionParameters
type ServiceBag = ams.Bag

func CountryFromStorefront(storefront string) string {
	return storekit.CountryFromStorefront(storefront)
}

// LegacySession holds data imported from the legacy session format.
type LegacySession struct {
	OwnerId    string
	Account    Account
	Parameters SessionParameters
	Secrets    SessionSecrets
	Amd        []byte
}

func ReadLegacySession(raw []byte) (LegacySession, error) {
	s, err := storekit.ReadLegacy(raw)
	if err != nil {
		return LegacySession{}, err
	}
	c := &Client{s: s}
	secrets, err := c.SessionSecrets()
	if err != nil {
		return LegacySession{}, err
	}
	amd, err := s.AmdState()
	return LegacySession{OwnerId: c.OwnerId(), Account: c.Account(), Parameters: c.SessionParameters(), Secrets: secrets, Amd: amd}, err
}

func (c *Client) SessionParameters() SessionParameters { return c.s.Parameters() }

func (c *Client) SessionSecrets() (SessionSecrets, error) { return c.s.Secrets() }

// Finish all client operations before dropping credentials and cookies.
func (c *Client) ForgetSecrets() { c.s.ForgetSecrets() }

func (c *Client) HttpClient() *http.Client { return c.s.Client }

// OpenSession combines persisted device identity with caller-supplied metadata and secrets.
func (a *Authenticator) OpenSession(account Account, owner string, p SessionParameters, secrets SessionSecrets, transport *http.Client) (*Client, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.state = nil // Always reread the store; the application may have replaced the device.
	if err := a.loadState(); err != nil {
		return nil, err
	}
	identity := storekit.Account{Email: account.Email, Name: account.Name, DirectoryServicesId: owner, Guid: a.state.guid()}
	if transport == nil {
		transport = storekit.NewSessionHttpClientWithTransport(a.transport)
	}
	s, err := storekit.OpenSession(transport, identity, p, secrets)
	if err != nil {
		return nil, err
	}
	a.transport = s.Client.Transport
	provider, err := a.state.amdProvider()
	if err != nil {
		return nil, err
	}
	if err = s.ConfigureAmd(&ams.AmdController{Provider: provider, Bag: p.Bag}, a.options.requestContext(p.Storefront, a.now())); err != nil {
		return nil, err
	}
	return &Client{s: s}, nil
}

func (a *Authenticator) SaveRegistration(c *Client) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.state == nil {
		return errors.New("Apple device not loaded")
	}
	raw, err := c.s.AmdState()
	if err != nil {
		return err
	}
	if raw == nil {
		return nil
	}
	next := *a.state
	next.Amd = raw
	if err = saveInstallation(a.options.Store, &next); err != nil {
		return err
	}
	a.state = &next
	return nil
}
