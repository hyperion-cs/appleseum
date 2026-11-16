package storekit

import (
	"net/http"
	"testing"

	"github.com/hyperion-cs/appleseum/applekit/internal/ams"
	"github.com/hyperion-cs/appleseum/applekit/internal/applebackemu"
	"github.com/hyperion-cs/appleseum/applekit/internal/httpx"
)

const (
	fixtureDispatchUrl       = "https://downloaddispatch.itunes.apple.com/WebObjects/DownloadDispatch.woa/wa/ent/download"
	fixtureAssetUrl          = "https://download.itunes.apple.com/asset"
	fixtureConfiguratorAgent = "Configurator/2.21 Configurator/2.21 (Macintosh; OS X 26.5; 25F71) AppleWebKit/1624.2.5.11.4 AMS/1 (dt:1)"
)

func downloadAccount() Account {
	return Account{Guid: "001122AABBCC", DirectoryServicesId: "123", Email: "fixture@example.test", StoreFront: "143517-2,34", PasswordToken: "token"}
}

// Local instrumentation only: all responses still come from applebackemu.
// Actor selection is client state and is deliberately observed outside the server.
type downloadProfileObserver struct {
	next   http.RoundTripper
	agents []string
}

func (o *downloadProfileObserver) RoundTrip(r *http.Request) (*http.Response, error) {
	o.agents = append(o.agents, httpx.UserAgentFor(r.Context()))
	return o.next.RoundTrip(r)
}

func observeDownloadProfiles(s *Session) *downloadProfileObserver {
	o := &downloadProfileObserver{next: s.Client.Transport}
	s.Client.Transport = o
	return o
}

func (o *downloadProfileObserver) check(t *testing.T, count int) {
	t.Helper()
	if len(o.agents) != count {
		t.Fatal("download did not cover expected exchanges", len(o.agents), count)
	}
	for _, agent := range o.agents {
		if agent != httpx.DefaultConfiguratorUserAgent {
			t.Fatal("download inherited a different parent actor")
		}
	}
}

func downloadBackend(t *testing.T, mode applebackemu.Mode, account Account, endpoint string, steps ...applebackemu.Step) (*Session, *applebackemu.Server) {
	t.Helper()
	s, backend := catalogBackend(t, mode, steps...)
	s.Account = account
	s.hardware, s.amd.Bag = []byte{0, 17, 34, 51, 68, 85}, ams.Bag{DownloadURL: endpoint}
	return s, backend
}

func dispatchStep(t *testing.T, account Account, endpoint string, id int64, keybag, version string, response applebackemu.Response) applebackemu.Step {
	t.Helper()
	step, err := applebackemu.DownloadProduct(applebackemu.DownloadOptions{
		Endpoint: endpoint, Guid: account.Guid, Dsid: account.DirectoryServicesId, Storefront: account.StoreFront, Token: account.PasswordToken,
		AppId: id, Keybag: keybag, Version: version,
	}, response)
	if err != nil {
		t.Fatal(err)
	}
	return step
}

func modernDownloadStep(step applebackemu.Step, language string) applebackemu.Step {
	step.Header.Set("User-Agent", fixtureConfiguratorAgent)
	step.Header.Set("Accept", "*/*")
	step.Header.Set("Accept-Encoding", "br, gzip, deflate")
	step.Header.Set("Accept-Language", language)
	step.Header.Set("Priority", "u=3, i")
	step.Header.Set("X-Apple-Client-Application", "com.apple.configurator.ui")
	step.Header.Set("X-Apple-Tz", "0")
	return step
}

func downloadReply(t *testing.T, fields map[string]any) applebackemu.Response {
	t.Helper()
	response, err := applebackemu.PlistResponse(fields)
	if err != nil {
		t.Fatal(err)
	}
	return response
}
