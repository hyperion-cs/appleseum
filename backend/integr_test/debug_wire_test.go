package integration_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	server "appleseum/backend/internal/app"
	"appleseum/backend/internal/wire"
)

func TestDebugWireCapturesRejectedBodyAndFinalHeaders(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "packages"), 0700); err != nil {
		t.Fatal(err)
	}
	app, err := newTestServer(server.Config{DataDir: dir, Debug: true, DebugLogDir: filepath.Join(dir, "logs")})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	httpServer := httptest.NewUnstartedServer(app.Handler())
	httpServer.Listener = app.DebugListener(httpServer.Config, httpServer.Listener)
	httpServer.Start()
	defer httpServer.Close()
	client := httpServer.Client()
	var state wire.State
	response, err := client.Get(httpServer.URL + "/api/state")
	if err != nil {
		t.Fatal(err)
	}
	json.NewDecoder(response.Body).Decode(&state)
	response.Body.Close()
	// Invalid CSRF is rejected before the JSON decoder reads the login body.
	payload := `{"email":"request@example.invalid","password":"unread-body-marker","code":""}`
	req, _ := http.NewRequest("POST", httpServer.URL+"/api/login", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Test-Header", "header-marker")
	response, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	actualHeaders := response.Header.Clone()
	actualBody, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 403 {
		t.Fatal("request not rejected")
	}
	req, _ = http.NewRequest("GET", httpServer.URL+"/api/debug?source=API&limit=5", nil)
	req.Header.Set("X-CSRF-Token", state.Csrf)
	response, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var page wire.DebugPage
	json.NewDecoder(response.Body).Decode(&page)
	response.Body.Close()
	if len(page.Items) != 1 || !page.Items[0].HasResponse {
		t.Fatal("exchange not finalized")
	}
	req, _ = http.NewRequest("GET", httpServer.URL+"/api/debug/responses/"+strconv.FormatInt(page.Items[0].Id, 10)+"?raw=1", nil)
	req.Header.Set("X-CSRF-Token", state.Csrf)
	response, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	exchange, _ := io.ReadAll(response.Body)
	response.Body.Close()
	parts := bytes.SplitN(exchange, []byte("\r\n\r\n----- RESPONSE -----\r\n"), 2)
	if len(parts) != 2 {
		t.Fatal("exchange framing missing")
	}
	capturedRequest, err := http.ReadRequest(bufio.NewReader(bytes.NewReader(parts[0])))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(capturedRequest.Body)
	capturedRequest.Body.Close()
	if string(body) != payload || capturedRequest.Header.Get("X-Test-Header") != "header-marker" {
		t.Fatal("unread request or header lost")
	}
	capturedResponse, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(parts[1])), capturedRequest)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(capturedResponse.Body)
	capturedResponse.Body.Close()
	if !bytes.Equal(body, actualBody) {
		t.Fatal("response body differs")
	}
	for _, key := range []string{"Date", "Content-Length", "Content-Type"} {
		if actualHeaders.Get(key) == "" || capturedResponse.Header.Get(key) != actualHeaders.Get(key) {
			t.Fatalf("final %s header missing or changed", key)
		}
	}
}
