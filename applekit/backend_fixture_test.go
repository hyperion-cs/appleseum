package applekit

import (
	"net/http"
	"testing"

	"github.com/hyperion-cs/appleseum/applekit/internal/applebackemu"
	"github.com/hyperion-cs/appleseum/applekit/internal/httpx"
)

func sdkReply(t *testing.T, fields map[string]any) applebackemu.Response {
	t.Helper()
	response, err := applebackemu.PlistResponse(fields)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func midStep(t *testing.T, state *installation, endpoint string, input map[string]any, response applebackemu.Response) applebackemu.Step {
	t.Helper()
	header := http.Header{
		"User-Agent":      {"akd/1.0 CFNetwork/3860.600.21 Darwin/25.5.0"},
		"X-Mme-Device-Id": {state.deviceId()},
	}
	step, err := applebackemu.PlistRequest(endpoint, input, header, response)
	if err != nil {
		t.Fatal(err)
	}
	return step
}

// This observes local actor selection and forwards all remote behavior unchanged.
type sdkActorObserver struct {
	next   http.RoundTripper
	agents []string
}

func (o *sdkActorObserver) RoundTrip(r *http.Request) (*http.Response, error) {
	o.agents = append(o.agents, httpx.UserAgentFor(r.Context()))
	return o.next.RoundTrip(r)
}

func (o *sdkActorObserver) checkAuthKit(t *testing.T, count int) {
	t.Helper()
	if len(o.agents) != count {
		t.Fatal("unexpected local actor call count", len(o.agents))
	}
	for _, agent := range o.agents {
		if agent != httpx.DefaultAuthKitUserAgent {
			t.Fatal("MidService lost AuthKit actor")
		}
	}
}
