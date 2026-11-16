package scripts

import (
	"bytes"
	"compress/gzip"
	"crypto/tls"
	"encoding/json"
	"flag"
	"io"
	"maps"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

var smokeBinary = flag.String("binary", "", "path to the built executable; enables the standalone smoke test")

type smokePreferences struct {
	Language, Theme string
	Revision        int
}

type smokeState struct {
	Csrf        string
	Mode        struct{ Admin, Debug, Public, Service bool }
	Preferences smokePreferences
	PageSizes   json.RawMessage
	Tables      map[string]struct{ Value map[string]json.RawMessage }
}

type smokeJournal struct {
	Items []struct{ Id int64 }
}

type smokeProcess struct {
	Capacity                     int
	Runtime, Version, Executable string
	Arguments                    []string
}

type smokeDevice struct {
	Id, Guid           string
	Settings, Defaults map[string]any
}

type smokeExchange struct {
	Request  struct{ PostData struct{ Text string } }
	Response struct {
		Status      int
		HttpVersion string
		Headers     []struct{ Name, Value string }
	}
}

type smokeArchive struct {
	Log struct {
		Version string
		Entries []smokeExchange
	}
}

type smokeClient struct {
	t            *testing.T
	http         *http.Client
	base         string
	csrf         string
	rootPassword string
}

func smokeJson[T any](t *testing.T, data []byte) T {
	t.Helper()
	var value T
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func (c *smokeClient) request(method, target string, body any, status int, headers ...http.Header) ([]byte, http.Header) {
	c.t.Helper()
	var data []byte
	if body != nil {
		data = mustSmokeMarshal(c.t, body)
	}
	if strings.HasPrefix(target, "/") {
		target = c.base + target
	}
	req, err := http.NewRequest(method, target, bytes.NewReader(data))
	if err != nil {
		c.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", c.csrf)
	for _, extra := range headers {
		for name, values := range extra {
			req.Header.Del(name)
			for _, value := range values {
				req.Header.Add(name, value)
			}
		}
	}
	response, err := c.http.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer response.Body.Close()
	data, err = io.ReadAll(response.Body)
	if err != nil {
		c.t.Fatal(err)
	}
	if response.StatusCode != status {
		c.t.Fatalf("%s %s: status %d, want %d", method, target, response.StatusCode, status)
	}
	return data, response.Header
}

func (c *smokeClient) get(path string) []byte {
	c.t.Helper()
	data, _ := c.request("GET", path, nil, http.StatusOK)
	return data
}

func smokeAddresses(t *testing.T) []string {
	t.Helper()
	var addresses []string
	for range 2 {
		listener, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer listener.Close()
		addresses = append(addresses, listener.Addr().String())
	}
	return addresses
}

type smokeOutput struct {
	mu sync.Mutex
	bytes.Buffer
}

func (b *smokeOutput) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Buffer.Write(data)
}

func (b *smokeOutput) String() string { b.mu.Lock(); defer b.mu.Unlock(); return b.Buffer.String() }

func startSmoke(t *testing.T, binary, data string, addresses []string, debug bool, extraArgs ...string) *smokeClient {
	t.Helper()
	args := []string{"--data", data, "--http", addresses[0]}
	public := false
	for _, arg := range extraArgs {
		public = public || arg == "--public"
	}
	if !public {
		args = append(args, "--https", addresses[1])
	}
	args = append(args, extraArgs...)
	if debug {
		args = append(args, "--settings", "--debug")
	}
	command := exec.Command(binary, args...)
	command.Dir = filepath.Dir(binary)
	var output smokeOutput
	command.Stdout, command.Stderr = &output, &output
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	var waitErr error
	go func() { waitErr = command.Wait(); close(done) }()
	t.Cleanup(func() {
		select {
		case <-done:
			t.Errorf("server exited before cleanup: %v\n%s", waitErr, output.String())
			return
		default:
		}
		// Windows cannot send os.Interrupt to a child process.
		if runtime.GOOS == "windows" {
			command.Process.Kill()
		} else if err := command.Process.Signal(os.Interrupt); err != nil {
			t.Error(err)
			command.Process.Kill()
		}
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			command.Process.Kill()
			<-done
			t.Error("graceful shutdown timed out")
		}
		if waitErr != nil && runtime.GOOS != "windows" {
			t.Errorf("server shutdown: %v\n%s", waitErr, output.String())
		}
	})
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	transport := &http.Transport{
		DisableCompression: true,
		TLSClientConfig:    &tls.Config{InsecureSkipVerify: true}, // Isolated server uses a self-signed manifest certificate.
	}
	t.Cleanup(transport.CloseIdleConnections)
	c := &smokeClient{t: t, base: "http://" + addresses[0], http: &http.Client{
		Timeout: 2 * time.Second, Jar: jar, Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-done:
			t.Fatalf("server startup: %v\n%s", waitErr, output.String())
		default:
		}
		response, err := c.http.Get(c.base + "/api/health")
		if err == nil {
			io.Copy(io.Discard, response.Body)
			response.Body.Close()
			if response.StatusCode == http.StatusOK {
				if public {
					for _, line := range strings.Split(output.String(), "\n") {
						if value, ok := strings.CutPrefix(strings.TrimSpace(line), "Password: "); ok {
							c.rootPassword = value
						}
					}
					if len(c.rootPassword) != 12 {
						time.Sleep(10 * time.Millisecond)
						continue
					}
				}
				return c
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("server startup timed out")
	return nil
}

func smokeHar(c *smokeClient, source string) (smokeExchange, string) {
	c.t.Helper()
	journal := smokeJson[smokeJournal](c.t, c.get("/api/debug?source="+source))
	if len(journal.Items) == 0 {
		c.t.Fatalf("missing %s exchange", source)
	}
	path := "/api/debug/responses/" + strconv.FormatInt(journal.Items[0].Id, 10)
	archive := smokeJson[smokeArchive](c.t, c.get(path))
	if archive.Log.Version != "1.2" || len(archive.Log.Entries) != 1 {
		c.t.Fatal("invalid HAR export")
	}
	exchange := archive.Log.Entries[0]
	for _, header := range exchange.Response.Headers {
		if strings.EqualFold(header.Name, "Date") && header.Value != "" {
			return exchange, path
		}
	}
	c.t.Fatal("HAR response has no Date header")
	return smokeExchange{}, ""
}

func mustSmokeMarshal(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestStandaloneBinary(t *testing.T) {
	if *smokeBinary == "" {
		t.Skip("build the executable, then run go test ./scripts -count=1 -args -binary ../build/appleseum")
	}
	source := *smokeBinary
	if runtime.GOOS == "windows" && filepath.Ext(source) == "" {
		source += ".exe"
	}
	input, err := os.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	directory := t.TempDir()
	binary := filepath.Join(directory, filepath.Base(source))
	destination, err := os.OpenFile(binary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0700)
	if err != nil {
		t.Fatal(err)
	}
	_, copyErr := io.Copy(destination, input)
	closeErr := destination.Close()
	if copyErr != nil || closeErr != nil {
		t.Fatal(copyErr, closeErr)
	}
	data := filepath.Join(directory, "nested", "data")
	addresses := smokeAddresses(t)
	if !t.Run("default", func(t *testing.T) {
		c := startSmoke(t, binary, data, addresses, false)
		if info, err := os.Stat(filepath.Join(data, "packages")); err != nil || !info.IsDir() {
			t.Fatal("first launch did not create package storage", err)
		}
		for _, path := range []string{"/", "/login", "/available", "/downloads", "/downloaded", "/profile", "/appstore", "/localLibrary", "/debug", "/settings"} {
			for _, encoding := range []string{"identity", "gzip"} {
				body, header := c.request("GET", path, nil, 200, http.Header{"Accept-Encoding": {encoding}})
				if encoding == "gzip" {
					if header.Get("Content-Encoding") != "gzip" {
						t.Fatalf("%s: missing gzip encoding", path)
					}
					reader, err := gzip.NewReader(bytes.NewReader(body))
					if err != nil {
						t.Fatal(err)
					}
					body, err = io.ReadAll(reader)
					reader.Close()
					if err != nil {
						t.Fatal(err)
					}
				}
				if !strings.HasPrefix(header.Get("Content-Type"), "text/html") || !bytes.Contains(body, []byte("<title>Appleseum</title>")) {
					t.Fatalf("%s: invalid HTML response", path)
				}
			}
		}
		state := smokeJson[smokeState](t, c.get("/api/state"))
		c.csrf = state.Csrf
		if state.Preferences != (smokePreferences{"en", "dark", 1}) || state.Mode.Admin || state.Mode.Debug {
			t.Fatal("unexpected initial preferences or feature flags")
		}
		for _, path := range []string{"/api/debug", "/api/admin/device", "/data/metadata.db", "/data/library.db", "/.lock", "/data/.lock"} {
			c.request("GET", path, nil, 404)
		}
		c.request("PUT", "/api/preferences", map[string]string{"language": "ru", "theme": "light"}, 200)
		c.request("GET", "https://"+addresses[1]+"/api/state", nil, 404)
	}) {
		return
	}
	t.Run("debug and settings", func(t *testing.T) {
		c := startSmoke(t, binary, data, addresses, true)
		state := smokeJson[smokeState](t, c.get("/api/state"))
		c.csrf = state.Csrf
		if state.Preferences != (smokePreferences{"ru", "light", 2}) || !state.Mode.Admin || !state.Mode.Debug {
			t.Fatal("preferences were not preserved or feature flags were not enabled")
		}
		if info, err := os.Stat(filepath.Join(data, "logs")); err != nil || !info.IsDir() {
			t.Fatal("journal directory missing from data")
		}
		if _, err := os.Stat(filepath.Join(directory, "logs")); !os.IsNotExist(err) {
			t.Fatal("journal must not be beside the executable")
		}
		snapshot := smokeJson[smokeProcess](t, c.get("/api/debug/info"))
		if snapshot.Capacity != 1_000 || len(smokeJson[smokeJournal](t, c.get("/api/debug")).Items) != 0 || snapshot.Runtime == "" || snapshot.Version == "" {
			t.Fatal("invalid initial debug snapshot")
		}
		if snapshot.Executable != binary || len(snapshot.Arguments) == 0 || snapshot.Arguments[len(snapshot.Arguments)-1] != "--debug" {
			t.Fatal("debug snapshot lost process metadata")
		}
		c.get("/debug")
		rejected := map[string]string{"language": "en", "theme": "dark"}
		c.request("PUT", "/api/preferences", rejected, 403, http.Header{"X-CSRF-Token": {"wrong-token"}})
		exchange, path := smokeHar(c, "API")
		if exchange.Response.Status != 403 || !reflect.DeepEqual(smokeJson[map[string]string](t, []byte(exchange.Request.PostData.Text)), rejected) {
			t.Fatal("HAR lost the rejected request or response")
		}
		wire := c.get(path + "?raw=1")
		if !bytes.Contains(wire, mustSmokeMarshal(t, rejected)) || !bytes.Contains(bytes.ToLower(wire), []byte("content-length:")) {
			t.Fatal("raw export lost the request body or headers")
		}
		c.request("GET", "/packages/missing/missing.ipa", nil, 404)
		exchange, _ = smokeHar(c, "Hosting")
		if exchange.Response.Status != 404 {
			t.Fatal("shared HTTP listener did not capture IPA request")
		}
		c.request("GET", "https://"+addresses[1]+"/wire-test", nil, 404)
		exchange, _ = smokeHar(c, "Hosting")
		if exchange.Response.Status != 404 || exchange.Response.HttpVersion != "HTTP/1.1" {
			t.Fatal("invalid manifest wire capture")
		}
		device := smokeJson[smokeDevice](t, c.get("/api/admin/device"))
		if device.Id == "" || device.Settings["model"] == "" || device.Settings["osVersion"] == "" || device.Settings["locale"] != "en-US" || device.Settings["timeZone"] != "UTC" {
			t.Fatal("new device is missing initial settings")
		}
		custom := maps.Clone(device.Settings)
		custom["locale"], custom["timeZone"] = "en-GB", "Europe/London"
		body, _ := c.request("PUT", "/api/admin/device", custom, 200)
		changed := smokeJson[smokeDevice](t, body)
		if changed.Id != device.Id || !reflect.DeepEqual(changed.Settings, custom) {
			t.Fatal("settings update changed identity or failed to save")
		}
		body, _ = c.request("POST", "/api/admin/device/defaults", nil, 200)
		defaults := smokeJson[smokeDevice](t, body)
		if (defaults.Id != device.Id) != (changed.Settings["model"] != device.Defaults["model"]) || !reflect.DeepEqual(defaults.Settings, device.Defaults) {
			t.Fatal("restoring defaults changed identity incorrectly or failed to save")
		}
		body, _ = c.request("POST", "/api/admin/device/reset", nil, 200)
		reset := smokeJson[smokeDevice](t, body)
		if reset.Id == "" || reset.Id == defaults.Id || reset.Guid == "" || reset.Guid == defaults.Guid {
			t.Fatal("device reset did not replace identity")
		}
		c.get("/settings")
		c.request("DELETE", "/api/debug", nil, http.StatusNoContent)
		if len(smokeJson[smokeJournal](t, c.get("/api/debug")).Items) != 0 {
			t.Fatal("journal was not cleared")
		}
		if state.PageSizes != nil {
			t.Fatal("legacy pageSizes returned")
		}
		for _, table := range state.Tables {
			if _, exists := table.Value["limit"]; exists {
				t.Fatal("page limit persisted in table preferences")
			}
		}
		output, err := exec.Command(binary, "--help").CombinedOutput()
		if err != nil || !bytes.Contains(output, []byte("Appleseum v"+snapshot.Version)) {
			t.Fatalf("help: %v\n%s", err, output)
		}
		for _, expected := range []string{
			"Your apps are here. Even apps delisted from the App Store – install them on iPhone/iPad over your local network, without a cable.",
			"--http PORT|IP:PORT", "frontend, our API and IPA files", "--https PORT|IP:PORT", "--public", "--shared-library", "Requires --public.",
			"Cannot be combined with --debug=web or --https.",
			"If only a port is specified, the server listens on all network interfaces.", "Default: 8080\n", "Default: 8443; disabled with --public\n", "Default: ./data\n",
			"Requires a reverse proxy with a valid HTTPS certificate for the entire utility.",
		} {
			if !bytes.Contains(output, []byte(expected)) {
				t.Fatalf("help is missing agreed text: %s", expected)
			}
		}
		for _, removed := range []string{"\n  --host ", "--extra-host", "|off", "-packages", "-manifest", "Commands:", "appleseum init"} {
			if bytes.Contains(output, []byte(removed)) {
				t.Fatalf("removed option or command remains in help: %s", removed)
			}
		}
	})
	t.Run("public", func(t *testing.T) {
		publicData := filepath.Join(directory, "public-data")
		c := startSmoke(t, binary, publicData, addresses, false, "--public", "--shared-library", "--settings", "--debug=raw")
		upstream, err := url.Parse(c.base)
		if err != nil {
			t.Fatal(err)
		}
		proxy := httptest.NewTLSServer(httputil.NewSingleHostReverseProxy(upstream))
		defer proxy.Close()
		c.base = proxy.URL
		state := smokeJson[smokeState](t, c.get("/api/state"))
		c.csrf = state.Csrf
		if !state.Mode.Public || state.Mode.Service || state.Mode.Debug || !state.Mode.Admin {
			t.Fatal("invalid public startup flags")
		}
		c.get("/login")
		c.request("POST", "/api/login", map[string]string{"email": "root@appleseum", "password": c.rootPassword}, 200)
		state = smokeJson[smokeState](t, c.get("/api/state"))
		if !state.Mode.Service || state.Mode.Admin || state.Mode.Debug {
			t.Fatal("root acquired Apple settings")
		}
		c.get("/api/library")
		c.request("GET", "/api/catalog", nil, 401)
		c.request("GET", "/api/admin/device", nil, 404)
		c.request("GET", "/manifests/missing.plist", nil, 404)
		for _, name := range []string{"local.key", "logs"} {
			if _, err = os.Stat(filepath.Join(publicData, name)); !os.IsNotExist(err) {
				t.Fatal("public mode created local key or debug logs", err)
			}
		}
		if connection, err := net.DialTimeout("tcp", addresses[1], time.Second); err == nil {
			connection.Close()
			t.Fatal("public mode opened a second listener")
		}
		for _, host := range []string{"apps.example.test", "apps.example.test:9443", "[2001:db8::1]:443", "203.0.113.10"} {
			req, _ := http.NewRequest("GET", c.base+"/api/health", nil)
			req.Host = host
			response, err := c.http.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			if response.StatusCode != 200 {
				t.Fatal("valid public Host rejected", host)
			}
		}
		c.request("POST", "/api/logout", nil, 204)
	})
	if info, err := os.Stat(filepath.Join(data, "metadata.db")); err != nil || info.IsDir() {
		t.Fatal("metadata database missing")
	}
	if _, err := os.Stat(filepath.Join(data, "library.db")); !os.IsNotExist(err) {
		t.Fatal("legacy database created")
	}
	for _, option := range []struct{ name, value string }{
		{"--http", "off"}, {"--http", "65536"}, {"--https", "invalid"},
		{"--https", "off"}, {"--extra-host", "apps.example.test"},
	} {
		unused := filepath.Join(directory, "invalid-options")
		output, err := exec.Command(binary, option.name, option.value, "--data", unused).CombinedOutput()
		if err == nil || !bytes.Contains(output, []byte(strings.TrimLeft(option.name, "-"))) {
			t.Fatalf("invalid option accepted: %s %s", option.name, option.value)
		}
		if _, err := os.Stat(unused); !os.IsNotExist(err) {
			t.Fatal("invalid options created a data directory", err)
		}
	}
	for _, conflict := range [][]string{{"--public", "--debug"}, {"--public", "--https", addresses[1]}, {"--shared-library"}} {
		unused := filepath.Join(directory, "conflicting-options")
		args := append(conflict, "--data", unused)
		output, err := exec.Command(binary, args...).CombinedOutput()
		if err == nil || (!bytes.Contains(output, []byte("cannot be combined")) && !bytes.Contains(output, []byte("requires --public"))) {
			t.Fatalf("conflicting flags accepted: %v: %s", args, output)
		}
		if _, err = os.Stat(unused); !os.IsNotExist(err) {
			t.Fatal("conflicting flags changed runtime data")
		}
	}
	blocked := filepath.Join(directory, "blocked")
	marker := []byte("synthetic existing file")
	if err := os.WriteFile(blocked, marker, 0600); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command(binary, "--data", blocked).CombinedOutput(); err == nil || !bytes.Contains(output, []byte("create data directory")) {
		t.Fatalf("invalid data directory did not stop startup: %v\n%s", err, output)
	}
	if contents, err := os.ReadFile(blocked); err != nil || !bytes.Equal(contents, marker) {
		t.Fatal("invalid data directory overwrote an existing file", err)
	}
}
