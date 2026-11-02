package tester

import (
	"context"
	"errors"
	"net"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"
)

func testScopedEngine(t *testing.T, v4, v6 []string, source string) *Engine {
	t.Helper()
	iface := &net.Interface{Name: "test0", Index: 42}
	sources, err := chooseInterfaceSources(iface, v4, v6, source)
	if err != nil {
		t.Fatal(err)
	}
	e := &Engine{iface: iface, sources: sources, resolver: newDNSResolver(), ctx: context.Background(), dialer: &net.Dialer{}}
	e.resolver.sourceCheck = e.checkSource
	return e
}

func TestOnlyLinkLocalIPv6FiltersDNSAndRetainsLinkLocal(t *testing.T) {
	e := testScopedEngine(t, []string{"192.0.2.4"}, []string{"fe80::1"}, "")
	lookups := 0
	e.resolver.lookupIP = func(context.Context, string) ([]net.IP, error) {
		lookups++
		return []net.IP{net.ParseIP("2001:db8::20"), net.ParseIP("192.0.2.20"), net.ParseIP("fe80::20")}, nil
	}
	for _, want := range []string{"192.0.2.20", "fe80::20", "192.0.2.20", "fe80::20"} {
		ip, err := e.resolver.lookup(context.Background(), "dual.example")
		if err != nil || ip.String() != want {
			t.Fatalf("lookup = %v, %v; want %s", ip, err, want)
		}
	}
	if lookups != 1 {
		t.Fatalf("lookups = %d", lookups)
	}
	var dials int
	blocked := errors.New("test: no network")
	e.dialer.Control = func(network, address string, _ syscall.RawConn) error {
		dials++
		if network != "tcp4" || address != "192.0.2.20:80" {
			t.Errorf("unexpected dial: %s %s", network, address)
		}
		return blocked
	}
	if _, _, err := e.connect(target{domain: "dual.example", port: 80}); !errors.Is(err, blocked) {
		t.Fatalf("dual-stack IPv4 dial did not reach control: %v", err)
	}
	ip, err := e.resolver.lookup(context.Background(), "fe80::20%test0")
	if err != nil {
		t.Fatalf("link-local literal: %v", err)
	}
	network, source, err := e.dialSource(ip)
	if err != nil || network != "tcp6" || source.IP.String() != "fe80::1" || source.Zone != "test0" {
		t.Fatalf("link-local source: %s %v %v", network, source, err)
	}
	for _, domain := range []string{"fe80::20", "fe80::20%test0"} {
		host, err := e.targetHost(domain, ip)
		if err != nil || host != "fe80::20%test0" {
			t.Fatalf("host = %q, %v", host, err)
		}
	}
	if _, err := e.targetHost("fe80::20%other", ip); err == nil {
		t.Fatal("mismatched destination zone accepted")
	}
	if _, err := e.resolver.lookup(context.Background(), "2001:db8::20"); err == nil || !strings.Contains(err.Error(), "链路本地") {
		t.Fatalf("global IPv6 literal error: %v", err)
	}
	e.resolver.lookupIP = func(context.Context, string) ([]net.IP, error) { return []net.IP{net.ParseIP("2001:db8::20")}, nil }
	if _, _, err := e.connect(target{domain: "global-only.example", port: 80}); err == nil || !strings.Contains(err.Error(), "作用域") {
		t.Fatalf("global-only DNS error: %v", err)
	}
	if dials != 1 {
		t.Fatalf("unexpected dials, got %d, want 1 IPv4 only", dials)
	}
}

func TestIPv6ScopeFilteringWithExplicitSource(t *testing.T) {
	for _, tc := range []struct {
		source string
		want   []string
	}{
		{"", []string{"fe80::20", "2001:db8::20", "fe80::20"}},
		{"fe80::1", []string{"fe80::20", "fe80::20", "fe80::20"}},
		{"2001:db8::1", []string{"2001:db8::20", "2001:db8::20", "2001:db8::20"}},
	} {
		e := testScopedEngine(t, nil, []string{"2001:db8::1", "fe80::1"}, tc.source)
		e.resolver.lookupIP = func(context.Context, string) ([]net.IP, error) {
			return []net.IP{net.ParseIP("fe80::20"), net.ParseIP("2001:db8::20")}, nil
		}
		for _, want := range tc.want {
			ip, err := e.resolver.lookup(context.Background(), "mixed.example")
			if err != nil || ip.String() != want {
				t.Fatalf("source %q: lookup %v, %v; want %s", tc.source, ip, err, want)
			}
			_, local, err := e.dialSource(ip)
			if err != nil || local == nil || local.IP.IsLinkLocalUnicast() != ip.IsLinkLocalUnicast() {
				t.Fatalf("source %q: destination %v, local %v, error %v", tc.source, ip, local, err)
			}
		}
	}
}

func TestIPv6SourceScopeSelection(t *testing.T) {
	v6 := []string{"2001:db8::1", "fe80::1", "fd00::1"}
	for _, tc := range []struct {
		source, destination, wantSource, wantZone string
		wantErr                                   bool
	}{
		{"", "2001:db8::20", "2001:db8::1", "", false},
		{"", "fd00::20", "2001:db8::1", "", false},
		{"", "fe80::20", "fe80::1", "test0", false},
		{"fe80::1", "fe80::20", "fe80::1", "test0", false},
		{"fe80::1", "2001:db8::20", "", "", true},
		{"2001:db8::1", "fe80::20", "", "", true},
		{"2001:db8::1", "2001:db8::20", "2001:db8::1", "", false},
	} {
		e := testScopedEngine(t, nil, v6, tc.source)
		ip := net.ParseIP(tc.destination)
		lookupIP, lookupErr := e.resolver.lookup(context.Background(), tc.destination)
		network, local, dialErr := e.dialSource(ip)
		if tc.wantErr {
			if lookupErr == nil || dialErr == nil || !strings.Contains(lookupErr.Error(), "链路本地") {
				t.Errorf("source %q target %q: lookup=%v dial=%v", tc.source, tc.destination, lookupErr, dialErr)
			}
			continue
		}
		if lookupErr != nil || !lookupIP.Equal(ip) || dialErr != nil || network != "tcp6" || local == nil || local.IP.String() != tc.wantSource || local.Zone != tc.wantZone {
			t.Errorf("source %q target %q: lookup=%v/%v dial=%s/%v/%v", tc.source, tc.destination, lookupIP, lookupErr, network, local, dialErr)
		}
	}
}

func TestInterfaceAddressFamiliesAndSources(t *testing.T) {
	addrs := []net.Addr{
		&net.IPNet{IP: net.ParseIP("2001:db8::2")},
		&net.IPNet{IP: net.ParseIP("192.0.2.4")},
		&net.IPAddr{IP: net.ParseIP("fe80::1")},
	}
	v4, v6 := splitInterfaceIPs(addrs)
	if !reflect.DeepEqual(v4, []string{"192.0.2.4"}) || !reflect.DeepEqual(v6, []string{"2001:db8::2", "fe80::1"}) {
		t.Fatalf("addresses: %v, %v", v4, v6)
	}
	iface := &net.Interface{Name: "test0", Index: 42}
	for _, tc := range []struct {
		source, zone string
		valid        bool
	}{
		{"192.0.2.4", "", true}, {"2001:db8::2", "", true},
		{"fe80::1", "test0", true}, {"fe80::1%test0", "test0", true},
		{"fe80::1%42", "test0", true}, {"fe80::1%other", "", false},
		{"2001:db8::3", "", false}, {"bad", "", false},
	} {
		local, err := validateSource(iface, v4, v6, tc.source)
		if (err == nil) != tc.valid {
			t.Errorf("%q: %v", tc.source, err)
			continue
		}
		if err == nil && local.Zone != tc.zone {
			t.Errorf("%q: zone = %q", tc.source, local.Zone)
		}
	}
	if _, _, _, err := selectInterface("", "::1"); err == nil {
		t.Fatal("source without interface accepted")
	}
	if _, _, _, err := selectInterface("auto", "::1"); err == nil {
		t.Fatal("source with auto interface accepted")
	}
}

func TestIPv6LoopbackDialAndLog(t *testing.T) {
	listener, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Skipf("IPv6 loopback unavailable: %v", err)
	}
	defer listener.Close()
	var loop string
	for _, iface := range ListInterfaces() {
		if iface.IsUp && iface.IsLoop {
			for _, ip := range iface.IPv6 {
				if ip == "::1" {
					loop = iface.Name
				}
			}
		}
	}
	if loop == "" {
		t.Skip("no IPv6 loopback interface")
	}
	for _, iface := range []string{"", loop} {
		cfg := baseConfig()
		cfg.Interface = iface
		cfg.Domains = []string{"::1"}
		e := newTestEngine(t, cfg)
		conn, _, err := e.connect(target{domain: "::1", port: listener.Addr().(*net.TCPAddr).Port})
		if err != nil {
			t.Fatalf("IPv6 connect interface %q: %v", iface, err)
		}
		conn.Close()
	}
	cfg := baseConfig()
	cfg.Domains = []string{"::1"}
	cfg.Interface = loop
	cfg.SourceIP = "::1"
	e := newTestEngine(t, cfg)
	port := listener.Addr().(*net.TCPAddr).Port
	conn, ip, err := e.connect(target{domain: "::1", port: port})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close()
	if ip != "::1" {
		t.Fatalf("IP = %q", ip)
	}
	if got := conn.LocalAddr().(*net.TCPAddr).IP.String(); got != "::1" {
		t.Fatalf("source = %q", got)
	}
	if _, _, err := e.connect(target{domain: "127.0.0.1", port: port}); err == nil {
		t.Fatal("IPv6 source allowed IPv4 destination")
	}
	e.recordResult(target{domain: "::1", port: port}, ip, time.Now(), true, nil)
	logs, _ := e.LogsSince(0, 10)
	if len(logs) != 1 || logs[0].IP != "::1" {
		t.Fatalf("logs = %+v", logs)
	}
	e.finish()
	rows := readResultCSV(t, exportTestResult(t, e).LogPath)
	if len(rows) != 1 || rows[0][2] != "::1" {
		t.Fatalf("CSV = %v", rows)
	}
}
