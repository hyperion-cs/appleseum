package hosting

import (
	"net"
	"testing"
)

func TestDockerBridgeNamesPreserveLanAndVpnInterfaces(t *testing.T) {
	for _, tc := range []struct {
		name   string
		docker bool
	}{
		{"docker0", true}, {"docker_gwbridge", true}, {"vEthernet (DockerNAT)", true},
		{"br-cf8ea74d7821", true}, {"br-012345ABCDEF", true},
		{"eth0", false}, {"en0", false}, {"enp2s0", false}, {"utun0", false},
		{"br-lan", false}, {"bridge0", false}, {"vEthernet (WSL)", false},
		{"br-123456789abc0", false}, {"br-0123456789xz", false},
	} {
		if dockerBridge(tc.name) != tc.docker {
			t.Errorf("unexpected console visibility for interface %q", tc.name)
		}
	}
}

func TestExplicitListenersSkipInterfaceDiscovery(t *testing.T) {
	for _, tt := range []struct {
		name      string
		addresses []string
		want      []string
	}{
		{"HTTP and HTTPS", []string{"127.0.0.1:8080", "127.0.0.1:8443"}, []string{"127.0.0.1", "127.0.0.1"}},
		{"public HTTP only", []string{"127.0.0.1:8080"}, []string{"127.0.0.1"}},
		{"IPv4 and IPv6", []string{"127.0.0.1:8080", "[::1]:8443"}, []string{"127.0.0.1", "::1"}},
		{"IPv4 loopback range", []string{"127.0.0.2:8080"}, []string{"127.0.0.2"}},
		{"HTTPS still exposed", []string{"127.0.0.1:8080", "0.0.0.0:8443"}, nil},
		{"HTTP still exposed", []string{"0.0.0.0:8080", "127.0.0.1:8443"}, nil},
		{"IPv6 wildcard", []string{"[::]:8080"}, nil},
		{"empty host", []string{":8080"}, nil},
		{"LAN listener", []string{"192.168.1.2:8080"}, []string{"192.168.1.2"}},
		{"distinct explicit listeners", []string{"192.168.1.2:8080", "127.0.0.1:8443"}, []string{"192.168.1.2", "127.0.0.1"}},
		{"IPv6 listener", []string{"[2001:db8::2]:8080"}, []string{"2001:db8::2"}},
		{"unvalidated address", []string{"8080"}, nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := explicitListenIps(tt.addresses...)
			if len(got) != len(tt.want) {
				t.Fatalf("explicit listen addresses = %v, want %v", got, tt.want)
			}
			for i, want := range tt.want {
				if !got[i].Equal(net.ParseIP(want)) {
					t.Fatalf("explicit listen address = %s, want %s", got[i], want)
				}
			}
		})
	}
}
