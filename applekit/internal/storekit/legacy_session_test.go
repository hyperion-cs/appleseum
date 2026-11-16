package storekit

import (
	"cmp"
	"encoding/json"
	"fmt"
	"slices"
	"time"
)

// Synthetic legacy snapshots exercise session migration.
func (s *Session) Export() ([]byte, error) {
	jar, ok := s.Client.Jar.(*persistentJar)
	if !ok {
		return nil, fmt.Errorf("session has no persistent cookie jar")
	}
	configurator, err := s.exportConfigurator()
	if err != nil {
		return nil, err
	}
	s.metaMu.Lock()
	authenticatedAt, lastSuccessAt := s.authenticatedAt, s.lastSuccessAt
	s.metaMu.Unlock()
	jar.mu.Lock()
	state := sessionData{Version: 1, Account: s.Account, DownloadUrl: s.amd.Bag.DownloadURL, RedownloadUrl: s.amd.Bag.RedownloadURL, Configurator: configurator, AuthenticatedAt: authenticatedAt, LastSuccessAt: lastSuccessAt}
	now := time.Now()
	for key, c := range jar.cookies {
		if !c.Cookie.Expires.IsZero() && !c.Cookie.Expires.After(now) {
			delete(jar.cookies, key)
			continue
		}
		state.Cookies = append(state.Cookies, c)
	}
	jar.mu.Unlock()
	slices.SortStableFunc(state.Cookies, func(a, b savedCookie) int { return cmp.Compare(a.Created, b.Created) })
	return json.Marshal(state)
}

func Restore(raw []byte) (*Session, error) { return ReadLegacy(raw) }

func (s *Session) exportConfigurator() (*savedConfiguratorState, error) {
	if s.amd.Provider == nil {
		return nil, nil
	}
	raw, err := s.AmdState()
	if err != nil {
		return nil, err
	}
	return &savedConfiguratorState{Amd: raw, Bag: s.amd.Bag, Context: s.metadata}, nil
}
