package hosting

import (
	"encoding/hex"
	"net"
	"strings"
)

type localIpv4 struct {
	name string
	ip   net.IP
}

// Docker bridges stay in the complete inventory, but not the suggested links.
func interfaceAddresses() (all, preferred []net.IP, err error) {
	all = []net.IP{net.IPv4(127, 0, 0, 1)}
	preferred = append(preferred, all...)
	addresses, err := localIpv4s()
	if err != nil {
		return nil, nil, err
	}
	for _, address := range addresses {
		ip := address.ip
		if !ip.IsLoopback() && !ip.IsUnspecified() {
			all = append(all, ip)
			if !dockerBridge(address.name) {
				preferred = append(preferred, ip)
			}
		}
	}
	return all, preferred, nil
}

func dockerBridge(name string) bool {
	if name == "docker0" || name == "docker_gwbridge" || name == "vEthernet (DockerNAT)" {
		return true
	}
	suffix, ok := strings.CutPrefix(name, "br-")
	if !ok || len(suffix) != 12 {
		return false
	}
	_, err := hex.DecodeString(suffix)
	return err == nil
}
