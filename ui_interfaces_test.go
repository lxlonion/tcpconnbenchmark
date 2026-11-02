package main

import (
	"fmt"
	"image/png"
	"os"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"tcpconnbenchmark/internal/tester"
)

func interfaceView(s *viewStub) *nativeView {
	v := newNativeView(s)
	v.interfaces = []tester.InterfaceInfo{
		{Name: "en0", IsUp: true, IPv4: []string{"192.0.2.10"}, IPv6: []string{"2001:db8::10", "fe80::1"}},
		{Name: "utun0", IsUp: true, IPv6: []string{"2001:db8::20"}},
		{Name: "down0", IPv6: []string{"2001:db8::30"}},
		{Name: "lo0", IsUp: true, IsLoop: true, IPv6: []string{"::1"}},
	}
	return v
}

func TestInterfacePickerTreeAndSourceSelection(t *testing.T) {
	s := &viewStub{}
	v := interfaceView(s)
	tt := ui.NewTester(v.view, 900, 650)
	if err := tt.Click("选择出口网卡"); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"网络接口 en0", "IPv4 · 192.0.2.10", "IPv6 · 2001:db8::10", "IPv6 · fe80::1", "网络接口 utun0", "IPv6 · 2001:db8::20"} {
		if !tt.HasText(text) {
			t.Fatalf("missing %q: %v", text, tt.Texts())
		}
	}
	for _, text := range []string{"网络接口 down0", "网络接口 lo0"} {
		if tt.HasText(text) {
			t.Fatalf("unavailable interface shown: %s", text)
		}
	}
	parent := assertInViewport(t, tt, "网络接口 en0", 900, 650)
	child := assertInViewport(t, tt, "en0 IPv6 · 2001:db8::10", 900, 650)
	if child.Y <= parent.Y {
		t.Fatal("addresses must appear below their interface")
	}
	if path := os.Getenv("TCPCONNBENCHMARK_INTERFACE_SCREENSHOT"); path != "" {
		f, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		err = png.Encode(f, tt.Image())
		closeErr := f.Close()
		if err != nil || closeErr != nil {
			t.Fatalf("screenshot: %v %v", err, closeErr)
		}
	}
	if err := tt.Click("en0 IPv6 · 2001:db8::10"); err != nil {
		t.Fatal(err)
	}
	if v.iface != "en0" || v.sourceIP != "2001:db8::10" || v.interfacePickerOpen {
		t.Fatalf("incorrect selection: %q %q open=%v", v.iface, v.sourceIP, v.interfacePickerOpen)
	}
	if err := tt.Click("开始测试"); err != nil {
		t.Fatal(err)
	}
	if len(s.requests) != 1 || s.requests[0].Interface != "en0" || s.requests[0].SourceIP != "2001:db8::10" {
		t.Fatalf("source selection not passed to start: %+v", s.requests)
	}
}

func TestInterfacePickerSelectsIPv4AndLinkLocal(t *testing.T) {
	for _, address := range []struct{ family, ip string }{{"IPv4", "192.0.2.10"}, {"IPv6", "fe80::1"}} {
		t.Run(address.ip, func(t *testing.T) {
			v := interfaceView(&viewStub{})
			tt := ui.NewTester(v.view, 900, 650)
			if err := tt.Click("选择出口网卡"); err != nil {
				t.Fatal(err)
			}
			if err := tt.Click("en0 " + address.family + " · " + address.ip); err != nil {
				t.Fatal(err)
			}
			if v.iface != "en0" || v.sourceIP != address.ip || !v.interfaceSelectionAvailable() {
				t.Fatalf("invalid source selection: %q %q", v.iface, v.sourceIP)
			}
		})
	}
}

func TestInterfacePickerCanChooseInterfaceAndAutomatic(t *testing.T) {
	v := interfaceView(&viewStub{})
	v.sourceIP, v.iface = "192.0.2.10", "en0"
	tt := ui.NewTester(v.view, 1180, 780)
	if err := tt.Click("选择出口网卡"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("网络接口 utun0"); err != nil {
		t.Fatal(err)
	}
	if v.iface != "utun0" || v.sourceIP != "" {
		t.Fatalf("interface selection retained source: %q %q", v.iface, v.sourceIP)
	}
	if err := tt.Click("选择出口网卡"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("自动（系统默认路由）"); err != nil {
		t.Fatal(err)
	}
	if v.iface != "" || v.sourceIP != "" {
		t.Fatal("automatic selection did not clear source and interface")
	}
}

func TestInterfaceRefreshChecksSelectedAddress(t *testing.T) {
	for _, tc := range []struct {
		name string
		ips  []string
		keep bool
	}{
		{"unchanged", []string{"2001:db8::10", "2001:db8::11"}, true},
		{"removed", []string{"2001:db8::11"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := interfaceView(&viewStub{})
			v.iface, v.sourceIP = "en0", "2001:db8::10"
			v.interfacesPending = reply([]tester.InterfaceInfo{{Name: "en0", IsUp: true, IPv6: tc.ips}}, nil)
			v.poll(time.Now())
			if tc.keep {
				if v.iface != "en0" || v.sourceIP != "2001:db8::10" {
					t.Fatal("refresh lost a valid source selection")
				}
			} else if v.iface != "" || v.sourceIP != "" {
				t.Fatal("refresh retained an address that no longer exists")
			}
		})
	}
}

func TestInterfacePickerLockedDuringRun(t *testing.T) {
	v := interfaceView(&viewStub{})
	v.session = "running"
	v.drained = true
	v.interfacePickerOpen = true
	tt := ui.NewTester(v.view, 900, 650)
	if err := tt.Click("选择出口网卡"); err != nil {
		t.Fatal(err)
	}
	if v.interfacePickerOpen || tt.HasText("网络接口 en0") {
		t.Fatal("interface picker must remain closed while running")
	}
}

func TestInterfacePickerKeyboardAndDismiss(t *testing.T) {
	v := interfaceView(&viewStub{})
	tt := ui.NewTester(v.view, 900, 650)
	if err := tt.Click("选择出口网卡"); err != nil {
		t.Fatal(err)
	}
	tt.Key(0, ui.KeyEscape)
	if v.interfacePickerOpen {
		t.Fatal("Escape did not dismiss interface picker")
	}
	if err := tt.Click("选择出口网卡"); err != nil {
		t.Fatal(err)
	}
	// Clicking a branch arrow collapses the group without choosing an interface.
	r, ok := tt.Find("网卡 en0")
	if !ok {
		t.Fatal("missing interface branch")
	}
	tt.ClickAt(r.X+12, r.Y+r.H/2)
	if tt.HasText("IPv6 · 2001:db8::10") || !v.interfacePickerOpen || v.iface != "" {
		t.Fatalf("collapsing the group changed the source selection: source=%q open=%v texts=%v", v.iface, v.interfacePickerOpen, tt.Texts())
	}
	tt.Key(0, ui.KeyRight)
	if !tt.HasText("IPv6 · 2001:db8::10") {
		t.Fatal("Right did not expand interface group")
	}
	tt.Key(0, ui.KeyRight)
	tt.Key(0, ui.KeyDown)
	tt.Key(0, ui.KeyEnter)
	if v.iface != "en0" || v.sourceIP != "2001:db8::10" || v.interfacePickerOpen {
		t.Fatalf("keyboard selection failed: %q %q open=%v", v.iface, v.sourceIP, v.interfacePickerOpen)
	}
}

func TestInterfacePickerScrollsMultipleIPv6Addresses(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprintf("dark=%v", dark), func(t *testing.T) {
			v := interfaceView(&viewStub{})
			v.interfaces = v.interfaces[:1]
			v.interfaces[0].IPv6 = nil
			for i := 1; i <= 30; i++ {
				v.interfaces[0].IPv6 = append(v.interfaces[0].IPv6, fmt.Sprintf("2001:db8::%x", i))
			}
			tt := ui.NewTester(v.view, 900, 650)
			tt.SetDark(dark)
			if err := tt.Click("选择出口网卡"); err != nil {
				t.Fatal(err)
			}
			r := assertInViewport(t, tt, "网卡 en0", 900, 650)
			tt.Scroll(r.X+r.W/2, r.Y+r.H/2, 0, 2000)
			label := "en0 IPv6 · 2001:db8::1e"
			assertInViewport(t, tt, label, 900, 650)
			if err := tt.Click(label); err != nil {
				t.Fatal(err)
			}
			if v.sourceIP != "2001:db8::1e" || v.iface != "en0" || v.interfacePickerOpen {
				t.Fatalf("scrolled selection failed: %q %q open=%v", v.iface, v.sourceIP, v.interfacePickerOpen)
			}
		})
	}
}
