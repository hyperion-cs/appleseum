package app

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hyperion-cs/appleseum/applekit/hosting"
	"howett.net/plist"
)

func TestIshLocalInstallationHttpFlow(t *testing.T) {
	const (
		share = "00000000-0000-4000-8000-000000000001"
		file  = "00000000-0000-4000-8000-000000000002"
	)

	dir := dataDir(t)
	hosts := (hosting.Config{Resolver: hosting.Nip, Ish: true, LocalHosts: []string{"localhost", "127.0.0.1"}}).Hosts()
	alias := hosts[len(hosts)-1]
	// Installation links name the HTTP port, so it must be known before the server starts.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, httpPort, _ := net.SplitHostPort(listener.Addr().String())
	s, err := New(Config{Resolver: hosting.Nip, Ish: true, DataDir: dir, Hosts: hosts, HttpPort: httpPort})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	db := database(t, dir)
	payload := []byte("synthetic package payload")
	if err := os.WriteFile(filepath.Join(dir, "packages", file+".ipa"), payload, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		"INSERT INTO accounts(id) VALUES('fixture')",
		fmt.Sprintf("INSERT INTO packages(id,account_id,app_id,name,created_at,bundle,version,size) VALUES('%s','fixture',1,'Synthetic app','2026-01-01','test.example.app','1',%d)", file, len(payload)),
		fmt.Sprintf("INSERT INTO shares(id,created_at) VALUES('%s','2026-01-01')", share),
		fmt.Sprintf("INSERT INTO share_packages(share_id,package_id) VALUES('%s','%s')", share, file),
	} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	front := httptest.NewUnstartedServer(s.Handler())
	front.Listener.Close()
	front.Listener = listener
	front.Start()
	defer front.Close()
	token := csrf(t, s)
	tlsServer := httptest.NewUnstartedServer(s.Distribution())
	tlsServer.TLS, err = hosting.Tls(hosts)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := tlsServer.TLS.GetCertificate(&tls.ClientHelloInfo{})
	if err != nil {
		t.Fatal(err)
	}
	tlsServer.TLS.Certificates = []tls.Certificate{*certificate}
	tlsServer.StartTLS()
	defer tlsServer.Close()
	client := tlsServer.Client()
	client.Transport.(*http.Transport).TLSClientConfig.ServerName = alias
	r, _ := http.NewRequest("GET", tlsServer.URL+"/manifests/"+share+".plist", nil)
	r.Host = net.JoinHostPort(alias, "8443")
	response, err := client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	var manifest struct {
		Items []struct {
			Assets []struct {
				Url string `plist:"url"`
			} `plist:"assets"`
		} `plist:"items"`
	}
	if _, err := plist.Unmarshal(body, &manifest); err != nil || response.StatusCode != 200 || len(manifest.Items) != 1 || len(manifest.Items[0].Assets) != 1 {
		t.Fatalf("manifest status=%d parse=%v", response.StatusCode, err)
	}
	ipa := manifest.Items[0].Assets[0].Url
	if !strings.HasPrefix(ipa, front.URL+"/packages/") {
		t.Fatalf("IPA must use numeric HTTP endpoint: %s", ipa)
	}
	for _, method := range []string{"GET", "HEAD"} {
		r, _ := http.NewRequest(method, ipa, nil)
		r.Header.Set("Range", "bytes=0-3")
		response, err := front.Client().Do(r)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != 206 || method == "GET" && string(body) != string(payload[:4]) || method == "HEAD" && len(body) != 0 {
			t.Fatalf("%s IPA status=%d body=%q", method, response.StatusCode, body)
		}
	}
	for _, host := range []string{alias, net.JoinHostPort(alias, httpPort), strings.ToUpper(alias) + ".", net.JoinHostPort(strings.ToUpper(alias)+".", "8443")} {
		for _, path := range []string{"/", "/api/state", "/api/library", "/api/library/export", "/api/shares", "/packages/" + share + "/" + file + ".ipa", "/manifests/" + share + ".plist"} {
			for _, method := range []string{"GET", "HEAD", "POST"} {
				r, _ := http.NewRequest(method, front.URL+path, strings.NewReader("{}"))
				r.Host = host
				r.Header.Set("Content-Type", "application/json")
				r.Header.Set("X-CSRF-Token", token)
				r.Header.Set("Origin", "http://"+host)
				r.Header.Set("X-Forwarded-Proto", "https")
				response, err := front.Client().Do(r)
				if err != nil {
					t.Fatal(err)
				}
				response.Body.Close()
				want := 400
				if strings.HasPrefix(path, "/packages/") {
					want = 404
				}
				if response.StatusCode != want {
					t.Fatalf("HTTP accepted alias: %s %s Host=%s returned %d, want %d", method, path, host, response.StatusCode, want)
				}
			}
		}
		for _, path := range []string{"/manifests/" + share + ".plist", "/packages/" + share + "/" + file + ".ipa", "/api/state", "/"} {
			for _, method := range []string{"GET", "HEAD"} {
				r, _ := http.NewRequest(method, tlsServer.URL+path, nil)
				r.Host = host
				response, err := client.Do(r)
				if err != nil {
					t.Fatal(err)
				}
				response.Body.Close()
				want := 404
				if strings.HasPrefix(path, "/manifests/") {
					want = 200
				}
				if response.StatusCode != want {
					t.Fatalf("HTTPS alias scope: %s %s Host=%s returned %d, want %d", method, path, host, response.StatusCode, want)
				}
			}
		}
	}
	// The distribution handler must require actual TLS, not a forwarded scheme.
	r = httptest.NewRequest("GET", "http://"+alias+"/manifests/"+share+".plist", nil)
	r.Header.Set("X-Forwarded-Proto", "https")
	w := httptest.NewRecorder()
	s.Distribution().ServeHTTP(w, r)
	if w.Code != 404 {
		t.Fatalf("plain HTTP distribution accepted alias: %d", w.Code)
	}
	for _, host := range []string{"7f000002.nip.io", "attacker." + alias, alias + ".attacker.example.test"} {
		r, _ := http.NewRequest("GET", tlsServer.URL+"/manifests/"+share+".plist", nil)
		r.Host = host
		response, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != 404 {
			t.Fatalf("HTTPS manifest accepted unlisted alias %s: %d", host, response.StatusCode)
		}
	}
	for _, host := range []string{"127.0.0.1", "localhost"} {
		r, _ := http.NewRequest("GET", front.URL+"/api/state", nil)
		r.Host = host
		response, err := front.Client().Do(r)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != 200 {
			t.Fatalf("local HTTP API rejected %s: %d", host, response.StatusCode)
		}
	}
	for _, host := range []string{"unlisted.example.test", "attacker.example.test"} {
		r := httptest.NewRequest("GET", "http://"+host+"/api/health", nil)
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code == 200 {
			t.Fatalf("accepted unlisted alias %s", host)
		}
	}
}

func TestIshRejectsPublicBeforeOpeningStorage(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "unused")
	_, err := New(Config{Ish: true, Public: true, DataDir: dir})
	if err == nil || err.Error() != "--public is not supported in iSH" {
		t.Fatalf("unexpected public mode error: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("iSH public mode touched storage")
	}
}

func TestBonjourHostPolicyAndCertificate(t *testing.T) {
	config := hosting.Config{Ish: true, BonjourName: "phone-example.local", LocalHosts: []string{"localhost", "127.0.0.1"}}
	hosts := config.Hosts()
	service, err := New(Config{Ish: true, DataDir: dataDir(t), Hosts: hosts, BonjourName: config.BonjourName})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	for _, host := range []string{"phone-example.local", "PHONE-EXAMPLE.LOCAL.", "phone-example.local:8080"} {
		request := httptest.NewRequest("GET", "http://"+host+"/api/health", nil)
		response := httptest.NewRecorder()
		service.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("Bonjour Host %s: status %d", host, response.Code)
		}
	}
	for _, host := range []string{"other.local", "phone-example.local.attacker.test", "7f000001.nip.io"} {
		request := httptest.NewRequest("GET", "http://"+host+"/api/health", nil)
		response := httptest.NewRecorder()
		service.Handler().ServeHTTP(response, request)
		if response.Code == http.StatusOK {
			t.Fatalf("unlisted Host %s was accepted", host)
		}
	}
	certificate, err := hosting.Tls(hosts)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := certificate.GetCertificate(&tls.ClientHelloInfo{ServerName: config.BonjourName})
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(encoded.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := leaf.VerifyHostname(config.BonjourName); err != nil {
		t.Fatal(err)
	}
	if err := leaf.VerifyHostname("other.local"); err == nil {
		t.Fatal("certificate covers an unlisted Bonjour name")
	}
}
