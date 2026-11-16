package rawlog

import (
	"context"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hyperion-cs/appleseum/applekit"
)

func TestDnsFailureKeepsTransportCauseWithoutUrl(t *testing.T) {
	err := &url.Error{
		Op:  "Get",
		URL: "https://private-credential@example.com/private-token",
		Err: &net.DNSError{
			Name:        "example.com",
			Server:      "192.0.2.53:53",
			IsTimeout:   true,
			IsTemporary: true,
			UnwrapErr:   &net.OpError{Op: "read", Net: "udp", Err: context.DeadlineExceeded},
		},
	}
	got := Error(err)
	for _, want := range []string{`name="example.com"`, `server="192.0.2.53:53"`, "timeout=true", "operation=read", "network=udp", "cause=deadline"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %q", want, got)
		}
	}
	if strings.Contains(got, "private-") || strings.Contains(got, "https://") {
		t.Fatalf("URL leaked: %s", got)
	}
}

func TestDnsFallbackRecordsAttemptsAndVpnHintWithoutUrl(t *testing.T) {
	path := filepath.Join(t.TempDir(), "debug.log")
	closeLog, err := Start(path)
	if err != nil {
		t.Fatal(err)
	}
	defer closeLog()
	observeDns(applekit.DnsEvent{Kind: "failure", Host: "example.com", Strategy: "system", Network: "system", Duration: time.Second,
		Err: &url.Error{URL: "https://private-credential@example.com/private-token", Err: context.DeadlineExceeded}})
	observeDns(applekit.DnsEvent{Kind: "fallback", Strategy: "system-tcp", Previous: "system", Provider: "system", Network: "tcp", VpnPossible: true})
	if err := closeLog(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"apple DNS failure", "apple DNS fallback", `host="example.com"`, "duration=1s", "error=deadline", "VPN", "trying the system DNS over TCP"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("missing %q in %s", want, data)
		}
	}
	if strings.Contains(string(data), "private-") {
		t.Fatal("DNS diagnostic leaked a URL")
	}
}
