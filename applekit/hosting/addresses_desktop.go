//go:build !android

package hosting

import "net"

func localIpv4s() ([]localIpv4, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	var result []localIpv4
	for _, iface := range interfaces {
		addresses, err := iface.Addrs()
		if err != nil {
			return nil, err
		}
		for _, address := range addresses {
			ip, _, err := net.ParseCIDR(address.String())
			if err == nil && ip.To4() != nil {
				result = append(result, localIpv4{iface.Name, ip})
			}
		}
	}
	return result, nil
}
