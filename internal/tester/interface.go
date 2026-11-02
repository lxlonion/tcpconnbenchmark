package tester

import (
	"errors"
	"fmt"
	"net"
	"os"
	"sort"
	"strings"
	"syscall"
)

// InterfaceInfo is the data the interface picker needs for one network
// interface.
type InterfaceInfo struct {
	Name   string
	Index  int
	IPv4   []string
	Label  string
	IsUp   bool
	IsLoop bool
}

// ListInterfaces returns usable network interfaces, with interfaces that have
// an IPv4 address first.
func ListInterfaces() []InterfaceInfo {
	ifs, err := net.Interfaces()
	if err != nil {
		return nil
	}
	result := make([]InterfaceInfo, 0, len(ifs))
	for _, iface := range ifs {
		ipv4 := interfaceIPv4s(&iface)
		info := InterfaceInfo{
			Name:   iface.Name,
			Index:  iface.Index,
			IPv4:   ipv4,
			IsUp:   iface.Flags&net.FlagUp != 0,
			IsLoop: iface.Flags&net.FlagLoopback != 0,
		}
		if len(ipv4) > 0 {
			info.Label = iface.Name + " · " + ipv4[0]
			if len(ipv4) > 1 {
				info.Label += fmt.Sprintf(" 等 %d 个", len(ipv4))
			}
		} else {
			info.Label = iface.Name + " · 无 IPv4"
		}
		result = append(result, info)
	}
	sort.SliceStable(result, func(i, j int) bool {
		ai := len(result[i].IPv4) > 0
		aj := len(result[j].IPv4) > 0
		if ai != aj {
			return ai
		}
		return result[i].Name < result[j].Name
	})
	return result
}

func interfaceIPv4s(iface *net.Interface) []string {
	addrs, err := iface.Addrs()
	if err != nil {
		return nil
	}
	var out []string
	for _, addr := range addrs {
		var ip net.IP
		switch v := addr.(type) {
		case *net.IPNet:
			ip = v.IP
		case *net.IPAddr:
			ip = v.IP
		}
		if ip == nil {
			continue
		}
		if ip4 := ip.To4(); ip4 != nil {
			out = append(out, ip4.String())
		}
	}
	sort.Strings(out)
	return out
}

func selectInterface(name string) (iface *net.Interface, localIP net.IP, label string, err error) {
	name = strings.TrimSpace(name)
	if name == "" || strings.EqualFold(name, "auto") {
		return nil, nil, "自动（系统默认路由）", nil
	}
	iface, err = net.InterfaceByName(name)
	if err != nil {
		return nil, nil, "", fmt.Errorf("找不到网卡 %q: %w", name, err)
	}
	if iface.Flags&net.FlagUp == 0 {
		return nil, nil, "", fmt.Errorf("网卡 %q 当前未启用", name)
	}
	ips := interfaceIPv4s(iface)
	label = iface.Name
	if len(ips) > 0 {
		localIP = net.ParseIP(ips[0]).To4()
		label += " · " + ips[0]
	} else {
		label += " · 无 IPv4（按设备绑定）"
	}
	return iface, localIP, label, nil
}

// interfaceControl returns a net.Dialer Control callback that binds the socket
// to iface. Permission errors are tolerated when a source address is also set,
// because the source address already selects the outgoing interface on most
// systems.
func interfaceControl(iface *net.Interface, localIP net.IP) func(network, address string, c syscall.RawConn) error {
	if iface == nil {
		return nil
	}
	return func(network, address string, c syscall.RawConn) error {
		var bindErr error
		if err := c.Control(func(fd uintptr) {
			bindErr = bindToInterface(fd, iface)
		}); err != nil {
			return err
		}
		if bindErr != nil {
			if localIP != nil && (errors.Is(bindErr, os.ErrPermission) || errors.Is(bindErr, syscall.EPERM)) {
				return nil
			}
			return fmt.Errorf("绑定网卡 %s 失败: %w", iface.Name, bindErr)
		}
		return nil
	}
}
