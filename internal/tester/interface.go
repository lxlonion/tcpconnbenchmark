package tester

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
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
	IPv6   []string
	Label  string
	IsUp   bool
	IsLoop bool
}

// ListInterfaces returns usable network interfaces, with interfaces that have
// an IP address first.
func ListInterfaces() []InterfaceInfo {
	ifs, err := net.Interfaces()
	if err != nil {
		return nil
	}
	result := make([]InterfaceInfo, 0, len(ifs))
	for _, iface := range ifs {
		ipv4, ipv6 := interfaceIPs(&iface)
		info := InterfaceInfo{
			Name:   iface.Name,
			Index:  iface.Index,
			IPv4:   ipv4,
			IPv6:   ipv6,
			IsUp:   iface.Flags&net.FlagUp != 0,
			IsLoop: iface.Flags&net.FlagLoopback != 0,
		}
		if len(ipv4) > 0 {
			info.Label = iface.Name + " · " + ipv4[0]
			if len(ipv4) > 1 {
				info.Label += fmt.Sprintf(" 等 %d 个", len(ipv4))
			}
		} else if len(ipv6) > 0 {
			info.Label = iface.Name + " · " + ipv6[0]
		} else {
			info.Label = iface.Name + " · 无 IP"
		}
		result = append(result, info)
	}
	sort.SliceStable(result, func(i, j int) bool {
		ai := len(result[i].IPv4)+len(result[i].IPv6) > 0
		aj := len(result[j].IPv4)+len(result[j].IPv6) > 0
		if ai != aj {
			return ai
		}
		return result[i].Name < result[j].Name
	})
	return result
}

func interfaceIPs(iface *net.Interface) ([]string, []string) {
	addrs, err := iface.Addrs()
	if err != nil {
		return nil, nil
	}
	return splitInterfaceIPs(addrs)
}

func splitInterfaceIPs(addrs []net.Addr) (v4, v6 []string) {
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
			v4 = append(v4, ip4.String())
		} else if ip.To16() != nil {
			v6 = append(v6, ip.String())
		}
	}
	sort.Strings(v4)
	sort.Strings(v6)
	return v4, v6
}

type interfaceSources struct {
	v4, v6, link6 *net.TCPAddr
}

func selectInterface(name, source string) (iface *net.Interface, sources interfaceSources, label string, err error) {
	name = strings.TrimSpace(name)
	source = strings.TrimSpace(source)
	if name == "" || strings.EqualFold(name, "auto") {
		if source != "" {
			return nil, sources, "", fmt.Errorf("指定源 IP 时必须选择网卡")
		}
		return nil, sources, "自动（系统默认路由）", nil
	}
	iface, err = net.InterfaceByName(name)
	if err != nil {
		return nil, sources, "", fmt.Errorf("找不到网卡 %q: %w", name, err)
	}
	if iface.Flags&net.FlagUp == 0 {
		return nil, sources, "", fmt.Errorf("网卡 %q 当前未启用", name)
	}
	ips4, ips6 := interfaceIPs(iface)
	sources, err = chooseInterfaceSources(iface, ips4, ips6, source)
	if err != nil {
		return nil, sources, "", err
	}
	if source != "" {
		return iface, sources, iface.Name + " · " + source, nil
	}
	label = iface.Name
	if sources.v4 != nil {
		label += " · " + sources.v4.IP.String()
	} else if sources.v6 != nil {
		label += " · " + sources.v6.IP.String()
	} else {
		label += " · " + sources.link6.IP.String()
	}
	return iface, sources, label, nil
}

// chooseInterfaceSources retains one source for each relevant family/scope.
// An explicitly selected source is the only source made available.
func chooseInterfaceSources(iface *net.Interface, ips4, ips6 []string, source string) (sources interfaceSources, err error) {
	if source != "" {
		local, sourceErr := validateSource(iface, ips4, ips6, source)
		if sourceErr != nil {
			return sources, sourceErr
		}
		if local.IP.To4() != nil {
			sources.v4 = local
		} else if local.IP.IsLinkLocalUnicast() {
			sources.link6 = local
		} else {
			sources.v6 = local
		}
		return sources, nil
	}
	if len(ips4) > 0 {
		sources.v4 = &net.TCPAddr{IP: net.ParseIP(ips4[0]).To4()}
	}
	for _, raw := range ips6 {
		ip := net.ParseIP(raw)
		if ip.IsLinkLocalUnicast() {
			if sources.link6 == nil {
				sources.link6 = &net.TCPAddr{IP: ip, Zone: iface.Name}
			}
		} else if sources.v6 == nil {
			sources.v6 = &net.TCPAddr{IP: ip}
		}
	}
	if sources.v4 == nil && sources.v6 == nil && sources.link6 == nil {
		return sources, fmt.Errorf("网卡 %q 没有可用 IP 地址", iface.Name)
	}
	return sources, nil
}

func (sources interfaceSources) forIP(ip net.IP) (*net.TCPAddr, error) {
	if ip.To4() != nil {
		if sources.v4 != nil {
			return sources.v4, nil
		}
	} else if ip.To16() != nil {
		if ip.IsLinkLocalUnicast() {
			if sources.link6 != nil {
				return sources.link6, nil
			}
			return nil, fmt.Errorf("目标 IPv6 为链路本地地址，但所选源 IP 不包含链路本地地址")
		}
		if sources.v6 != nil {
			return sources.v6, nil
		}
		return nil, fmt.Errorf("目标 IPv6 不是链路本地地址，但所选源 IP 不包含非链路本地 IPv6 地址")
	}
	return nil, fmt.Errorf("目标 IP 地址族与所选源地址不匹配")
}

func validateSource(iface *net.Interface, ips4, ips6 []string, source string) (*net.TCPAddr, error) {
	addr, err := netip.ParseAddr(source)
	if err != nil {
		return nil, fmt.Errorf("源 IP 无效: %q", source)
	}
	addr = addr.Unmap()
	if zone := addr.Zone(); zone != "" && zone != iface.Name && zone != fmt.Sprint(iface.Index) {
		return nil, fmt.Errorf("源 IP 的 zone 不属于网卡 %q", iface.Name)
	}
	plain := addr.WithZone("").String()
	ips := ips6
	if addr.Is4() {
		ips = ips4
	}
	for _, ip := range ips {
		if ip == plain {
			local := &net.TCPAddr{IP: net.ParseIP(plain)}
			if addr.Is6() && addr.IsLinkLocalUnicast() {
				local.Zone = iface.Name
			}
			return local, nil
		}
	}
	return nil, fmt.Errorf("源 IP %q 不属于网卡 %q", source, iface.Name)
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
			bindErr = bindToInterface(fd, iface, network)
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
