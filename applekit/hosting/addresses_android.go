//go:build android && cgo

package hosting

/*
#include <ifaddrs.h>
#include <netinet/in.h>
*/
import "C"

import (
	"net"
	"os"
	"unsafe"
)

// Android restricts the netlink requests used by net.Interfaces. Bionic's
// getifaddrs uses the platform's supported interface-discovery path (API 24+).
func localIpv4s() ([]localIpv4, error) {
	var head *C.struct_ifaddrs
	if result, err := C.getifaddrs(&head); result != 0 {
		return nil, os.NewSyscallError("getifaddrs", err)
	}
	defer C.freeifaddrs(head)
	var result []localIpv4
	for entry := head; entry != nil; entry = entry.ifa_next {
		if entry.ifa_addr == nil || entry.ifa_addr.sa_family != C.AF_INET {
			continue
		}
		address := (*C.struct_sockaddr_in)(unsafe.Pointer(entry.ifa_addr))
		bytes := (*[4]byte)(unsafe.Pointer(&address.sin_addr))
		ip := net.IPv4(bytes[0], bytes[1], bytes[2], bytes[3])
		result = append(result, localIpv4{C.GoString(entry.ifa_name), ip})
	}
	return result, nil
}
