//go:build windows

package tester

import (
	"math/bits"
	"net"

	"golang.org/x/sys/windows"
)

const ipUnicastIf = 31

func bindToInterface(fd uintptr, iface *net.Interface, network string) error {
	if network == "tcp6" {
		return windows.SetsockoptInt(windows.Handle(fd), windows.IPPROTO_IPV6, 31, iface.Index)
	}
	// Windows expects the interface index in network byte order for
	// IP_UNICAST_IF.
	index := bits.ReverseBytes32(uint32(iface.Index))
	return windows.SetsockoptInt(windows.Handle(fd), windows.IPPROTO_IP, ipUnicastIf, int(index))
}
