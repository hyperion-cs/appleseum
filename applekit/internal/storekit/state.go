package storekit

import (
	"cmp"
	"encoding"
	"encoding/hex"
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/hyperion-cs/appleseum/applekit/internal/ams"
	"github.com/hyperion-cs/appleseum/applekit/internal/httpx"
)

type SessionCookie = savedCookie

type SessionSecrets struct {
	PasswordToken string
	Cookies       []SessionCookie
}

type SessionParameters struct {
	Storefront      string
	Pod             string
	AuthenticatedAt time.Time
	LastSuccessAt   time.Time
	Bag             ams.Bag
}

func (s *Session) Parameters() SessionParameters {
	s.metaMu.Lock()
	defer s.metaMu.Unlock()
	p := SessionParameters{Storefront: s.Account.StoreFront, Pod: s.Account.Pod, AuthenticatedAt: s.authenticatedAt, LastSuccessAt: s.lastSuccessAt}
	p.Bag = s.amd.Bag
	return p
}

func (s *Session) Secrets() (SessionSecrets, error) {
	jar, ok := s.Client.Jar.(*persistentJar)
	if !ok {
		return SessionSecrets{}, errors.New("session has no persistent cookie jar")
	}
	state := SessionSecrets{PasswordToken: s.Account.PasswordToken}
	jar.mu.Lock()
	defer jar.mu.Unlock()
	now := time.Now()
	for key, c := range jar.cookies {
		if !c.Cookie.Expires.IsZero() && !c.Cookie.Expires.After(now) {
			delete(jar.cookies, key)
			continue
		}
		c.Cookie.Unparsed = slices.Clone(c.Cookie.Unparsed)
		state.Cookies = append(state.Cookies, c)
	}
	slices.SortStableFunc(state.Cookies, func(a, b savedCookie) int { return cmp.Compare(a.Created, b.Created) })
	return state, nil
}

// Preserve the supplied transport while restoring a private cookie jar.
func OpenSession(client *http.Client, account Account, p SessionParameters, secrets SessionSecrets) (*Session, error) {
	if account.DirectoryServicesId == "" || secrets.PasswordToken == "" || !httpx.IsAppleUrl(p.Bag.DownloadURL) || p.Bag.RedownloadURL != "" && !httpx.IsAppleUrl(p.Bag.RedownloadURL) {
		return nil, errors.New("invalid Apple session parameters")
	}
	hardware, err := hex.DecodeString(account.Guid)
	if err != nil || len(hardware) != 6 {
		return nil, errors.New("invalid Apple device GUID")
	}
	jar, err := restoreJar(secrets.Cookies)
	if err != nil {
		return nil, err
	}
	if client == nil {
		client = newSessionClient(jar)
	} else {
		copy := *client
		copy.Jar = jar
		client = &copy
	}
	account.PasswordToken, account.StoreFront, account.Pod = secrets.PasswordToken, p.Storefront, p.Pod
	return &Session{Account: account, Client: client, authenticatedAt: p.AuthenticatedAt, lastSuccessAt: p.LastSuccessAt, hardware: hardware, amd: ams.AmdController{Bag: p.Bag}}, nil
}

func (s *Session) ForgetSecrets() {
	s.Account.PasswordToken = ""
	s.Client.Jar = newJar()
}

func (s *Session) AmdState() ([]byte, error) {
	if s.amd.Provider == nil {
		return nil, nil
	}
	marshaler, ok := s.amd.Provider.(encoding.BinaryMarshaler)
	if !ok {
		return nil, errors.New("Configurator AMD provider is not persistable")
	}
	return marshaler.MarshalBinary()
}
