package hosting_test

import (
	"net"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/hyperion-cs/appleseum/applekit/hosting"
	"github.com/hyperion-cs/appleseum/applekit/ipa"
	"howett.net/plist"
)

func TestHostingInstallationAddresses(t *testing.T) {
	hosts := []string{"localhost", "127.0.0.1", "192.168.50.10", "::1"}
	for _, test := range []struct {
		name, host, peer, local, want string
		ordinary, public              bool
	}{
		{name: "loopback", host: "127.0.0.1", peer: "127.0.0.1", local: "127.0.0.1", want: "7f000001.nip.io"},
		{name: "localhost", host: "localhost", peer: "127.0.0.1", local: "127.0.0.1", want: "7f000001.nip.io"},
		{name: "same LAN address", host: "192.168.50.10", peer: "192.168.50.10", local: "192.168.50.10", want: "c0a8320a.nip.io"},
		{name: "another phone", host: "192.168.50.10", peer: "192.168.50.11", local: "192.168.50.10", want: "192.168.50.10"},
		{name: "another phone using alias", host: "c0a8320a.nip.io", peer: "192.168.50.11", local: "192.168.50.10", want: "192.168.50.10"},
		{name: "other own interface", host: "127.0.0.1", peer: "192.168.50.10", local: "127.0.0.1", want: "7f000001.nip.io"},
		{name: "IPv4 mapped IPv6", host: "192.168.50.10", peer: "::ffff:192.168.50.10", local: "192.168.50.10", want: "c0a8320a.nip.io"},
		{name: "IPv6 loopback", host: "[::1]", peer: "::1", local: "::1", want: "ipv6---1.nip.io"},
		{name: "ordinary platform", host: "127.0.0.1", peer: "127.0.0.1", local: "127.0.0.1", want: "127.0.0.1", ordinary: true},
		{name: "public", host: "apps.example.test", peer: "127.0.0.1", local: "127.0.0.1", want: "apps.example.test", public: true, ordinary: true},
		{name: "unknown local address", host: "192.168.50.20", peer: "192.168.50.20", local: "192.168.50.20", want: "192.168.50.20"},
		{name: "missing local address", host: "127.0.0.1", peer: "127.0.0.1", want: "127.0.0.1"},
		{name: "invalid peer", host: "127.0.0.1", peer: "invalid", local: "127.0.0.1", want: "127.0.0.1"},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := hosting.Config{Resolver: hosting.Nip, Ish: !test.ordinary, Public: test.public, LocalHosts: hosts, HttpPort: "8080", ManifestPort: "8443"}
			link, err := url.Parse(config.InstallUrl(hosting.Request{Host: test.host + ":8080", ClientIp: net.ParseIP(test.peer), LocalIp: net.ParseIP(test.local)}, "/manifests/share.plist"))
			if err != nil {
				t.Fatal(err)
			}
			manifest, err := url.Parse(link.Query().Get("url"))
			if err != nil {
				t.Fatal(err)
			}
			port := "8443"
			if test.public {
				port = "8080"
			}
			if manifest.Scheme != "https" || manifest.Host != net.JoinHostPort(test.want, port) {
				t.Fatalf("unexpected manifest address: %s", manifest)
			}
		})
	}
}

func TestHostingManifestKeepsNumericHttpPackage(t *testing.T) {
	for _, test := range []struct{ host, want string }{
		{"7f000001.nip.io", "127.0.0.1"},
		{"C0A8320A.NIP.IO.", "192.168.50.10"},
		{"192.168.50.10", "192.168.50.10"},
		{"ipv6---1.nip.io", "::1"},
	} {
		t.Run(test.host, func(t *testing.T) {
			h := hosting.Config{Resolver: hosting.Nip, Ish: true, LocalHosts: []string{"127.0.0.1", "192.168.50.10", "::1"}, HttpPort: "8081", ManifestPort: "8444"}
			apps := []hosting.App{{Metadata: ipa.Metadata{Bundle: "test.example.app", Version: "1"}, Path: "/files/app.ipa"}}
			body, err := h.Manifest(hosting.Request{Host: net.JoinHostPort(test.host, "8444")}, apps)
			if err != nil {
				t.Fatal(err)
			}
			var doc struct {
				Items []struct {
					Assets []struct {
						Url string `plist:"url"`
					} `plist:"assets"`
				} `plist:"items"`
			}
			if _, err := plist.Unmarshal(body, &doc); err != nil || len(doc.Items) != 1 || len(doc.Items[0].Assets) != 1 {
				t.Fatalf("manifest parse: %v", err)
			}
			want := "http://" + net.JoinHostPort(test.want, "8081") + "/files/app.ipa"
			if got := doc.Items[0].Assets[0].Url; got != want {
				t.Fatalf("got %s; want %s", got, want)
			}
		})
	}
}

func TestHostingHostsInventory(t *testing.T) {
	for _, test := range []struct {
		name        string
		ish, public bool
	}{{"ordinary", false, false}, {"ish", true, false}, {"public", false, true}} {
		t.Run(test.name, func(t *testing.T) {
			base := []string{"localhost", "127.0.0.1", "::1", "0.0.0.0", "192.168.50.10", "127.0.0.1"}
			before := append([]string(nil), base...)
			h := hosting.Config{Resolver: hosting.Nip, Ish: test.ish, Public: test.public, LocalHosts: base}
			want := append([]string(nil), base...)
			if test.ish {
				want = append(want, "7f000001.nip.io", "ipv6---1.nip.io", "c0a8320a.nip.io")
			}
			got := h.Hosts()
			if !reflect.DeepEqual(got, want) || !reflect.DeepEqual(base, before) {
				t.Fatalf("host inventory: %v", got)
			}
			got[0] = "changed.example.test"
			if !reflect.DeepEqual(base, before) {
				t.Fatal("returned hosts alias the input")
			}
		})
	}
}

func TestHostingManifestOnlyHosts(t *testing.T) {
	for _, test := range []struct {
		host string
		want bool
	}{
		{"7f000001.nip.io", true},
		{"ipv6---1.nip.io", true},
		{"nip.io", true},
		{"localhost", false},
		{"127.0.0.1", false},
		{"::1", false},
		{"7f000001.nip.io.attacker.example.test", false},
		{"notnip.io", false},
	} {
		if got := (hosting.Config{Resolver: hosting.Nip, Ish: true}).ManifestOnlyHost(test.host); got != test.want {
			t.Errorf("local host %s: got %t, want %t", test.host, got, test.want)
		}
		if (hosting.Config{Public: true}).ManifestOnlyHost(test.host) {
			t.Errorf("local alias policy applied to public host %s", test.host)
		}
	}
}

func TestHostingPublicManifest(t *testing.T) {
	h := hosting.Config{Public: true, HttpPort: "8080", ManifestPort: "8443"}
	body, err := h.Manifest(hosting.Request{Host: "apps.example.test:9443"}, []hosting.App{{Metadata: ipa.Metadata{Bundle: "test.example.app", Version: "1"}, Path: "/files/a b.ipa"}})
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Items []struct {
			Assets []struct {
				Url string `plist:"url"`
			} `plist:"assets"`
		} `plist:"items"`
	}
	if _, err := plist.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	if got := doc.Items[0].Assets[0].Url; got != "https://apps.example.test:9443/files/a%20b.ipa" {
		t.Fatal(got)
	}
}

func TestHostingPreservesTraceQuery(t *testing.T) {
	for _, public := range []bool{false, true} {
		h := hosting.Config{Public: public, HttpPort: "8080", ManifestPort: "8443"}
		request := hosting.Request{Host: "localhost:8080"}
		install, err := url.Parse(h.InstallUrl(request, "/manifests/share.plist?trace_id=abc1234"))
		if err != nil {
			t.Fatal(err)
		}
		manifest, err := url.Parse(install.Query().Get("url"))
		if err != nil || manifest.Path != "/manifests/share.plist" || manifest.Query().Get("trace_id") != "abc1234" {
			t.Fatalf("manifest URL: %v %v", manifest, err)
		}
		body, err := h.Manifest(request, []hosting.App{{Metadata: ipa.Metadata{Bundle: "test.app", Version: "1"}, Path: "/packages/share/file.ipa?trace_id=abc1234"}})
		if err != nil {
			t.Fatal(err)
		}
		var doc struct {
			Items []struct {
				Assets []struct {
					Url string `plist:"url"`
				} `plist:"assets"`
			} `plist:"items"`
		}
		if _, err := plist.Unmarshal(body, &doc); err != nil {
			t.Fatal(err)
		}
		asset, err := url.Parse(doc.Items[0].Assets[0].Url)
		if err != nil || asset.Path != "/packages/share/file.ipa" || asset.Query().Get("trace_id") != "abc1234" {
			t.Fatalf("package URL: %v %v", asset, err)
		}
	}
}

func TestBonjourInstallationAddresses(t *testing.T) {
	config := hosting.Config{Ish: true, BonjourName: "phone-example.local", LocalHosts: []string{"localhost", "127.0.0.1", "192.168.50.10", "::1"}, HttpPort: "8080", ManifestPort: "8443"}
	for _, test := range []struct {
		name, peer, local, want string
	}{
		{name: "same device IPv4", peer: "127.0.0.1", local: "127.0.0.1", want: "phone-example.local"},
		{name: "same device IPv6", peer: "::1", local: "::1", want: "phone-example.local"},
		{name: "same device LAN", peer: "192.168.50.10", local: "192.168.50.10", want: "phone-example.local"},
		{name: "other device", peer: "192.168.50.11", local: "192.168.50.10", want: "192.168.50.10"},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := hosting.Request{Host: "192.168.50.10:8080", ClientIp: net.ParseIP(test.peer), LocalIp: net.ParseIP(test.local)}
			link, _ := url.Parse(config.InstallUrl(request, "/manifests/share.plist"))
			manifest, _ := url.Parse(link.Query().Get("url"))
			if manifest.Hostname() != test.want || config.RequiresManifestHostCheck(request) {
				t.Fatalf("Bonjour link = %s; unexpected external DNS check", manifest)
			}
		})
	}
	for _, host := range config.Hosts() {
		if strings.HasSuffix(host, ".nip.io") {
			t.Fatalf("unexpected nip hostname %s", host)
		}
	}
	if !slices.Contains(config.Hosts(), config.BonjourName) || config.ManifestOnlyHost(config.BonjourName) {
		t.Fatal("Bonjour must be available to HTTP and HTTPS")
	}
}
