//go:build linux

package tester

import (
	"net"

	"golang.org/x/sys/unix"
)

func bindToInterface(fd uintptr, iface *net.Interface) error {
	// SO_BINDTODEVICE may require CAP_NET_RAW on recent kernels. The caller
	// tolerates EPERM when a source IPv4 address is configured as well.
	return unix.SetsockoptString(int(fd), unix.SOL_SOCKET, unix.SO_BINDTODEVICE, iface.Name)
}
