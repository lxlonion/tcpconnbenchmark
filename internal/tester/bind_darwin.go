//go:build darwin

package tester

import (
	"net"

	"golang.org/x/sys/unix"
)

func bindToInterface(fd uintptr, iface *net.Interface) error {
	return unix.SetsockoptInt(int(fd), unix.IPPROTO_IP, unix.IP_BOUND_IF, iface.Index)
}
