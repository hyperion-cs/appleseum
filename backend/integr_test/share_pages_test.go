package integration_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"

	"appleseum/backend/internal/wire"
)

func TestShareLimitAcrossBrowsersAndAtomicStop(t *testing.T) {
	app, ts, _ := publicApp(t)
	one, same, other, root := newBrowser(t, ts), newBrowser(t, ts), newBrowser(t, ts), newBrowser(t, ts)
	one.login("quota@example.test", "fixture")
	same.login("quota@example.test", "fixture")
	other.login("other@example.test", "fixture")
	ids := []string{}
	shares := []wire.Share{}
	for i := 1; i <= 9; i++ {
		ids = append(ids, one.download(fmt.Sprint(i)).Id)
	}
	for _, id := range ids[:8] {
		var share wire.Share
		one.request("POST", "/api/shares", wire.Selection{Ids: []string{id}}, 201, &share)
		shares = append(shares, share)
	}
	raw := same.request("POST", "/api/shares", wire.Selection{Ids: ids[8:]}, 409, nil)
	if !strings.Contains(string(raw), "sharing_limit") {
		t.Fatal("missing quota error")
	}
	if same.state().ShareCount != 8 {
		t.Fatal("account shares are scoped to a browser")
	}
	foreign := other.download("1")
	var foreignShare wire.Share
	other.request("POST", "/api/shares", wire.Selection{Ids: []string{foreign.Id}}, 201, &foreignShare)
	one.request("POST", "/api/shares/stop", wire.Selection{Ids: []string{shares[0].Id, foreignShare.Id}}, 404, nil)
	if one.state().ShareCount != 8 || other.state().ShareCount != 1 {
		t.Fatal("foreign selection partially stopped shares")
	}
	same.request("POST", "/api/shares/stop", wire.Selection{Ids: []string{shares[0].Id, shares[1].Id}}, 204, nil)
	same.request("POST", "/api/shares/stop", wire.Selection{Ids: []string{shares[0].Id}}, 204, nil)
	root.login("root@appleseum", app.ServicePassword())
	// Other users' shares do not consume the service account's own quota.
	for i := 1; i <= 8; i++ {
		root.request("POST", "/api/shares", wire.Selection{Ids: []string{ids[0], ids[i]}}, 201, nil)
	}
	root.request("POST", "/api/shares", wire.Selection{Ids: ids[:3]}, 409, nil)
	if root.state().ShareCount != 15 {
		t.Fatal("root cannot see all accounts' shares")
	}
	one.request("POST", "/api/logout", nil, 204, nil)
	one.state()
	one.login("quota@example.test", "fixture")
	if one.state().ShareCount != 6 {
		t.Fatal("logout detached share ownership")
	}
	root.request("POST", "/api/shares/stop", wire.Selection{Filter: &wire.Filter{}}, 204, nil)
	if root.state().ShareCount != 0 {
		t.Fatal("root failed to stop all shares")
	}
}

func TestShareLimitConcurrentCreation(t *testing.T) {
	_, ts, _ := publicApp(t)
	one, two := newBrowser(t, ts), newBrowser(t, ts)
	one.login("concurrent-quota@example.test", "fixture")
	two.login("concurrent-quota@example.test", "fixture")
	ids := []string{}
	for i := 1; i <= 9; i++ {
		ids = append(ids, one.download(fmt.Sprint(i)).Id)
	}
	for _, id := range ids[:7] {
		one.request("POST", "/api/shares", wire.Selection{Ids: []string{id}}, 201, nil)
	}
	var wg sync.WaitGroup
	codes := make(chan int, 2)
	for i, b := range []*browser{one, two} {
		wg.Add(1)
		go func(i int, b *browser) {
			defer wg.Done()
			body := fmt.Sprintf(`{"ids":[%q]}`, ids[7+i])
			req, err := newJsonRequest(b, "POST", "/api/shares", body)
			if err != nil {
				t.Error(err)
				return
			}
			resp, err := b.client.Do(req)
			if err != nil {
				t.Error(err)
				return
			}
			resp.Body.Close()
			codes <- resp.StatusCode
		}(i, b)
	}
	wg.Wait()
	close(codes)
	counts := map[int]int{}
	for code := range codes {
		counts[code]++
	}
	if counts[201] != 1 || counts[409] != 1 || one.state().ShareCount != 8 {
		t.Fatal("concurrent requests bypassed quota", counts)
	}
}

func TestSharePagesSearchScopeAndStableCursors(t *testing.T) {
	app, ts, dir := publicApp(t)
	owner, root, guest := newBrowser(t, ts), newBrowser(t, ts), newBrowser(t, ts)
	owner.login("pages@example.test", "fixture")
	first, second := owner.download("1"), owner.download("2")
	db := deviceDb(t, dir)
	for i := 0; i < 23; i++ {
		id := fmt.Sprintf("00000000-0000-4000-8000-%012d", i+1)
		if _, err := db.Exec("INSERT INTO shares(id,created_at,owner_id,owner_name) VALUES(?,'2026-11-10T12:00:00Z',?,?)", id, fmt.Sprintf("account:user%d", i/8), fmt.Sprintf("user%d@example.test", i/8)); err != nil {
			t.Fatal(err)
		}
		for _, file := range []string{first.Id, second.Id} {
			if _, err := db.Exec("INSERT INTO share_packages(share_id,package_id) VALUES(?,?)", id, file); err != nil {
				t.Fatal(err)
			}
		}
	}
	root.login("root@appleseum", app.ServicePassword())
	guest.request("GET", "/api/shares", nil, 401, nil)
	owner.request("GET", "/api/shares?sort=owner", nil, 400, nil)
	var hidden wire.SharePage
	owner.request("GET", "/api/shares", nil, 200, &hidden)
	if hidden.Total != 0 {
		t.Fatal("foreign shares leaked")
	}
	seen := map[string]bool{}
	cursor := ""
	var firstPage wire.SharePage
	for {
		var page wire.SharePage
		root.request("GET", "/api/shares?limit=5&sort=name&direction=asc&cursor="+url.QueryEscape(cursor), nil, 200, &page)
		if cursor == "" {
			firstPage = page
		}
		if page.Total != 23 || len(page.Items) > 5 {
			t.Fatal("unbounded or wrong page", page.Total, len(page.Items))
		}
		for _, row := range page.Items {
			if seen[row.Id] {
				t.Fatal("duplicate cursor row")
			}
			seen[row.Id] = true
			if row.Count != 2 || row.Owner == "" || row.CreatedAt == "" {
				t.Fatal("missing table fields")
			}
		}
		if page.Previous {
			var previous wire.SharePage
			root.request("GET", "/api/shares?limit=5&sort=name&direction=asc&cursor="+url.QueryEscape(page.Items[0].Cursor)+"&move=prev", nil, 200, &previous)
			if len(seen) == 10 && previous.Items[0].Id != firstPage.Items[0].Id {
				t.Fatal("backwards cursor skipped tied values")
			}
		}
		if !page.Next {
			break
		}
		cursor = page.Items[len(page.Items)-1].Cursor
	}
	if len(seen) != 23 {
		t.Fatal("keyset pagination missed rows", len(seen))
	}
	var found wire.SharePage
	root.request("GET", "/api/shares?q=App%2002", nil, 200, &found)
	if found.Total != 23 {
		t.Fatal("search ignored a non-leading app")
	}
	root.request("GET", "/api/shares?q=user2", nil, 200, &found)
	if found.Total != 7 {
		t.Fatal("owner search failed", found.Total)
	}
	root.request("GET", "/api/shares?q=changed&cursor="+url.QueryEscape(firstPage.Items[len(firstPage.Items)-1].Cursor), nil, 200, nil)
	root.request("GET", "/api/shares?offset=5", nil, 400, nil)
	raw := root.request("GET", "/api/state", nil, 200, nil)
	var state map[string]json.RawMessage
	json.Unmarshal(raw, &state)
	if _, ok := state["shares"]; ok {
		t.Fatal("state still carries the full share list")
	}
	root.request("POST", "/api/shares/stop", wire.Selection{Filter: &wire.Filter{Query: "user2"}}, 204, nil)
	if root.state().ShareCount != 16 {
		t.Fatal("filtered batch stop ignored search")
	}
}

func newJsonRequest(b *browser, method, path, body string) (*http.Request, error) {
	req, err := http.NewRequest(method, b.server.URL+path, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Host = publicHost
	req.Header.Set("Origin", "https://"+publicHost)
	req.Header.Set("X-CSRF-Token", b.csrf)
	req.Header.Set("X-Session-Key", b.key)
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}
