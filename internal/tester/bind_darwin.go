//go:build darwin

package tester

import (
	"net"

	"golang.org/x/sys/unix"
)

func bindToInterface(fd uintptr, iface *net.Interface, network string) error {
	if network == "tcp6" {
		return unix.SetsockoptInt(int(fd), unix.IPPROTO_IPV6, unix.IPV6_BOUND_IF, iface.Index)
	}
	return unix.SetsockoptInt(int(fd), unix.IPPROTO_IP, unix.IP_BOUND_IF, iface.Index)
}
