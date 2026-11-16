package integration_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"appleseum/backend/internal/wire"
)

func TestSharesRejectExactSetsAndAllowOverlap(t *testing.T) {
	dir := t.TempDir()
	h := launch(t, dir, nil)
	defer func() { h.close() }()
	ids := []string{}
	for i := 1; i <= 3; i++ {
		id := doImport(t, h.client, importRequest(t, h.http.URL, h.csrf, "file.ipa", importFixture(t, fmt.Sprintf("test.share.%d", i), fmt.Sprintf("App %d", i))), 201)
		ids = append(ids, id)
	}
	var single, triple, pair, otherPair, repeated wire.Share
	h.request(t, "POST", "/api/shares", wire.Selection{Ids: ids[:1]}, 201, &single)
	h.request(t, "POST", "/api/shares", wire.Selection{Ids: ids}, 201, &triple)
	h.request(t, "POST", "/api/shares", wire.Selection{Ids: ids[:2]}, 201, &pair)
	h.request(t, "POST", "/api/shares", wire.Selection{Ids: ids[1:]}, 201, &otherPair)
	if single.Id == triple.Id || pair.Id == otherPair.Id {
		t.Fatal("different sets were treated as identical")
	}
	for _, input := range []wire.Selection{{Ids: []string{ids[2], ids[0], ids[1]}}, {Filter: &wire.Filter{}}} {
		var failure struct{ Code string }
		h.request(t, "POST", "/api/shares", input, 409, &failure)
		if failure.Code != "sharing_duplicate" {
			t.Fatalf("identical set did not return a duplicate error: %+v", failure)
		}
	}
	var failure struct{ Code string }
	h.request(t, "POST", "/api/shares", wire.Selection{Filter: &wire.Filter{Query: "App 1"}}, 409, &failure)
	if failure.Code != "sharing_duplicate" {
		t.Fatal("selection by query bypassed set deduplication")
	}
	h.request(t, "POST", "/api/shares", wire.Selection{Ids: []string{ids[1], ids[0]}}, 409, &failure)
	if failure.Code != "sharing_duplicate" {
		t.Fatal("reordered set bypassed share deduplication")
	}
	var state wire.State
	h.request(t, "GET", "/api/state", nil, 200, &state)
	if state.ShareCount != 4 {
		t.Fatalf("wrong number of unique shares: %d", state.ShareCount)
	}
	h.request(t, "POST", "/api/shares/stop", wire.Selection{Ids: []string{single.Id}}, 204, nil)
	h.request(t, "POST", "/api/shares", wire.Selection{Ids: ids[:1]}, 201, &repeated)
	if repeated.Id == single.Id {
		t.Fatal("stopped share was reused")
	}
}

func TestConcurrentBrowserShareRequestsCreateOneShare(t *testing.T) {
	const count = 8

	_, ts, _ := publicAppWithSharedApps(t, true)
	one, two := newBrowser(t, ts), newBrowser(t, ts)
	one.login("one@example.test", "fixture")
	two.login("two@example.test", "fixture")
	item := one.download("1")
	browsers := []*browser{one, two}
	body, err := json.Marshal(wire.Selection{Ids: []string{item.Id}})
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		status int
		share  wire.Share
		code   string
		err    error
	}
	start, results := make(chan struct{}), make(chan result, count)
	for i := range count {
		b := browsers[i%len(browsers)]
		go func() {
			<-start
			request, err := http.NewRequest("POST", ts.URL+"/api/shares", bytes.NewReader(body))
			if err != nil {
				results <- result{err: err}
				return
			}
			request.Header.Set("Content-Type", "application/json")
			request.Host = publicHost
			request.Header.Set("Origin", "https://"+publicHost)
			request.Header.Set("X-CSRF-Token", b.csrf)
			response, err := b.client.Do(request)
			if err != nil {
				results <- result{err: err}
				return
			}
			defer response.Body.Close()
			got := result{status: response.StatusCode}
			if response.StatusCode == 409 {
				var failure struct{ Code string }
				got.err = json.NewDecoder(response.Body).Decode(&failure)
				got.code = failure.Code
			} else {
				got.err = json.NewDecoder(response.Body).Decode(&got.share)
			}
			results <- got
		}()
	}
	close(start)
	created, blocked, sharedId := 0, 0, ""
	for range count {
		got := <-results
		if got.err == nil && got.status == 409 && got.code == "sharing_duplicate" {
			blocked++
			continue
		}
		if got.err != nil || got.status != 201 || got.share.Id == "" {
			t.Fatalf("concurrent share failed: %+v", got)
		}
		if got.status == 201 {
			created++
		}
		if sharedId != "" && got.share.Id != sharedId {
			t.Fatal("concurrent requests created different shares")
		}
		sharedId = got.share.Id
	}
	shares := one.state().ShareCount + two.state().ShareCount
	if created != 1 || shares != 1 || blocked != count-1 {
		t.Fatalf("expected one creator and rejected all duplicate requests, got %d creations, %d active shares and %d rejections", created, shares, blocked)
	}
}
