//go:build !darwin && !linux && !windows

package tester

import "net"

func bindToInterface(fd uintptr, iface *net.Interface, network string) error {
	return nil
}
