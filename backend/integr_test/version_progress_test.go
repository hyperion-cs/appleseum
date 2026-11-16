package integration_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hyperion-cs/appleseum/applekit"

	"appleseum/backend/internal/wire"
)

func versionStream(t *testing.T, h *harness, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest("POST", h.http.URL+"/api/catalog/1/versions", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Accept", "application/x-ndjson")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", h.csrf)
	res, err := h.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { res.Body.Close() })
	if res.StatusCode != 200 || !strings.HasPrefix(res.Header.Get("Content-Type"), "application/x-ndjson") || res.Header.Get("X-Accel-Buffering") != "no" {
		t.Fatalf("not a progress stream: %d %v", res.StatusCode, res.Header)
	}
	return res
}

func readVersionEvents(t *testing.T, body io.Reader) []wire.VersionLoadEvent {
	t.Helper()
	decoder := json.NewDecoder(body)
	var events []wire.VersionLoadEvent
	for {
		var event wire.VersionLoadEvent
		if err := decoder.Decode(&event); err != nil {
			if err != io.EOF {
				t.Fatal(err)
			}
			return events
		}
		events = append(events, event)
	}
}

func TestVersionProgressArrivesBeforeResolutionFinishes(t *testing.T) {
	for _, debug := range []bool{false, true} {
		t.Run(map[bool]string{false: "normal", true: "debug response wrapper"}[debug], func(t *testing.T) {
			h, fake := launchVersions(t, debug)
			release := make(chan struct{})

			fake.tracked = func(ctx context.Context, id string, progress func(float64)) (applekit.VersionMetadata, error) {
				progress(40)
				select {
				case <-release:
				case <-ctx.Done():
					return applekit.VersionMetadata{}, ctx.Err()
				}
				progress(85)
				progress(100)
				return applekit.VersionMetadata{Version: "2.4.1", MinimumOs: "17.5"}, nil
			}

			res := versionStream(t, h, `{"limit":1}`)
			decoder := json.NewDecoder(res.Body)
			for _, want := range []int{0, 40} {
				var event wire.VersionLoadEvent
				if err := decoder.Decode(&event); err != nil || event.Progress != want || event.Page != nil || event.Error != "" {
					t.Fatalf("progress must arrive while resolver is blocked: %+v %v", event, err)
				}
			}
			close(release)
			// Continue through the same decoder: it may already buffer later frames.
			var got []int
			var result *wire.VersionPage
			for {
				var event wire.VersionLoadEvent
				err := decoder.Decode(&event)
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				got = append(got, event.Progress)
				result = event.Page
			}
			if !slices.Equal(got, []int{85, 99, 100}) || result == nil || len(result.Items) != 1 || result.Items[0].Version != "2.4.1" {
				t.Fatalf("progress=%v page=%+v", got, result)
			}
		})
	}
}

func TestVersionProgressCountsCachedRowsAndTheActualLastPage(t *testing.T) {
	h, _ := launchVersions(t)
	var first wire.VersionPage
	h.request(t, "POST", "/api/catalog/1/versions", wire.VersionPageRequest{Limit: 1}, 200, &first)
	res := versionStream(t, h, `{"limit":2}`)
	events := readVersionEvents(t, res.Body)
	var values []int
	for _, event := range events {
		values = append(values, event.Progress)
	}
	if !slices.Equal(values, []int{0, 50, 62, 87, 99, 100}) || events[len(events)-1].Page == nil {
		t.Fatalf("cached row progress: %+v", events)
	}
	raw, _ := json.Marshal([2]any{518, "1001"})
	body, _ := json.Marshal(wire.VersionPageRequest{Limit: 5, Cursor: base64.RawURLEncoding.EncodeToString(raw), Revision: first.Revision})
	res = versionStream(t, h, string(body))
	events = readVersionEvents(t, res.Body)
	values = nil
	for _, event := range events {
		values = append(values, event.Progress)
	}
	if !slices.Equal(values, []int{0, 25, 75, 99, 100}) || len(events[len(events)-1].Page.Items) != 1 {
		t.Fatalf("last page used the requested limit instead of actual rows: %+v", events)
	}
}

func TestVersionProgressErrorsAndDisconnects(t *testing.T) {
	t.Run("session expiry", func(t *testing.T) {
		h, fake := launchVersions(t)

		fake.info = func(context.Context, string) (applekit.VersionMetadata, error) {
			return applekit.VersionMetadata{}, applekit.ErrSessionExpired
		}

		res := versionStream(t, h, `{"limit":1}`)
		events := readVersionEvents(t, res.Body)
		if last := events[len(events)-1]; last.Error != "session_expired" || last.Page != nil || last.Progress == 100 {
			t.Fatalf("stream lost terminal error: %+v", events)
		}
	})
	t.Run("disconnect", func(t *testing.T) {
		h, fake := launchVersions(t)
		started, canceled := make(chan struct{}), make(chan struct{})

		fake.info = func(ctx context.Context, _ string) (applekit.VersionMetadata, error) {
			close(started)
			<-ctx.Done()
			close(canceled)
			return applekit.VersionMetadata{}, ctx.Err()
		}

		res := versionStream(t, h, `{"limit":5}`)
		<-started
		res.Body.Close()
		select {
		case <-canceled:
		case <-time.After(2 * time.Second):
			t.Fatal("closing the progress stream did not cancel metadata reads")
		}
	})
}
