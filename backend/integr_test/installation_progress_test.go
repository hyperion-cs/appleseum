package integration_test

import (
	"bytes"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"howett.net/plist"

	"appleseum/backend/internal/wire"
)

func TestInstallationProgressFlow(t *testing.T) {
	h := launch(t, t.TempDir(), nil)
	defer h.close()
	var ids []string
	sizes := map[string]int64{}
	var total int64
	for _, name := range []string{"One", "Two"} {
		data := importFixture(t, "test.progress."+name, name)
		id := doImport(t, h.client, importRequest(t, h.http.URL, h.csrf, "file.ipa", data), 201)
		ids = append(ids, id)
		sizes[id] = int64(len(data))
		total += int64(len(data))
	}
	var share wire.Share
	h.request(t, "POST", "/api/shares", wire.Selection{Ids: ids}, 201, &share)
	read := func(trace string) wire.InstallationProgress {
		t.Helper()
		var got wire.InstallationProgress
		raw := h.request(t, "GET", "/api/shares/"+share.Id+"/progress/"+trace, nil, 200, &got)
		if bytes.Contains(raw, []byte(`"files"`)) || bytes.Contains(raw, []byte(`"name"`)) {
			t.Fatal("progress exposes an unbounded file list")
		}
		return got
	}
	if got := read("abc1234"); got.Stage != wire.InstallWaiting {
		t.Fatal(got)
	}
	request := func(method, path, ranges string, status int) []byte {
		t.Helper()
		req, _ := http.NewRequest(method, path, nil)
		if ranges != "" {
			req.Header.Set("Range", ranges)
		}
		response, err := h.client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		if err != nil || response.StatusCode != status {
			t.Fatalf("delivery: %d %s %v", response.StatusCode, body, err)
		}
		return body
	}
	manifestPath := "/manifests/" + share.Id + ".plist?trace_id=abc1234"
	request("HEAD", h.dist.URL+manifestPath, "", 200)
	if read("abc1234").Stage != wire.InstallWaiting {
		t.Fatal("HEAD created an installation attempt")
	}
	raw := request("GET", h.dist.URL+manifestPath, "", 200)
	var manifest struct {
		Items []struct {
			Assets []struct {
				Url string `plist:"url"`
			} `plist:"assets"`
		} `plist:"items"`
	}
	if _, err := plist.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	got := read("abc1234")
	if got.Stage != wire.InstallWaitingIpa || got.Percent != 0 {
		t.Fatalf("manifest state: %+v", got)
	}
	var sent int64
	for i, item := range manifest.Items {
		asset, err := url.Parse(item.Assets[0].Url)
		if err != nil || asset.Query().Get("trace_id") != "abc1234" {
			t.Fatalf("trace missing: %v %v", asset, err)
		}
		path := h.http.URL + asset.RequestURI()
		request("HEAD", path, "", 200)
		if i == 0 && read("abc1234").Stage != wire.InstallWaitingIpa {
			t.Fatal("HEAD counted as an IPA download")
		}
		request("GET", path, "bytes=0-9", 206)
		first := read("abc1234").Percent
		expected := int(float64(sent+10) / float64(total) * 100)
		if first != expected {
			t.Fatalf("weighted progress: got %d, want %d", first, expected)
		}
		request("GET", path, "bytes=0-9", 206)
		if read("abc1234").Percent != first {
			t.Fatal("retry increased coverage")
		}
		request("GET", path, "", 200)
		id := strings.TrimSuffix(asset.Path[strings.LastIndex(asset.Path, "/")+1:], ".ipa")
		sent += sizes[id]
		if i == 0 && read("abc1234").Stage == wire.InstallComplete {
			t.Fatal("completed before all packages were sent")
		}
	}
	if got := read("abc1234"); got.Stage != wire.InstallComplete || got.Percent != 100 {
		t.Fatal("attempt not completed")
	}
	if got := read("xyz9876"); got.Stage != wire.InstallWaiting {
		t.Fatal("other trace saw progress")
	}
	request("GET", h.dist.URL+"/manifests/"+share.Id+".plist?trace_id=invalid!", "", 400)
	h.request(t, "GET", "/api/shares/"+share.Id+"/progress/invalid!", nil, 404, nil)
	h.request(t, "POST", "/api/shares/stop", wire.Selection{Ids: []string{share.Id}}, 204, nil)
	h.request(t, "GET", "/api/shares/"+share.Id+"/progress/abc1234", nil, 404, nil)
}

func TestInstallationProgressRespectsShareScope(t *testing.T) {
	_, ts, _ := publicAppWithSharedApps(t, true)
	one, two := newBrowser(t, ts), newBrowser(t, ts)
	one.login("one@example.test", "fixture")
	two.login("two@example.test", "fixture")
	item := one.download("1")
	var share wire.Share
	one.request("POST", "/api/shares", wire.Selection{Ids: []string{item.Id}}, 201, &share)
	one.request("GET", "/api/shares/"+share.Id+"/progress/abc1234", nil, 200, nil)
	two.request("GET", "/api/shares/"+share.Id+"/progress/abc1234", nil, 404, nil)
}
