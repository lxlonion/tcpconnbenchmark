package main

import (
	"strings"

	"github.com/egoist/mygo/ui"
)

func (v *nativeView) interfaceSelectionAvailable() bool {
	if v.iface == "" {
		return v.sourceIP == ""
	}
	for _, info := range v.interfaces {
		if info.Name != v.iface || !info.IsUp || info.IsLoop {
			continue
		}
		if v.sourceIP == "" {
			return true
		}
		for _, addresses := range [][]string{info.IPv4, info.IPv6} {
			for _, address := range addresses {
				if address == v.sourceIP {
					return true
				}
			}
		}
	}
	return false
}

func (v *nativeView) interfaceSelectionLabel() string {
	if v.iface == "" {
		return "自动（系统默认路由）"
	}
	if v.sourceIP == "" {
		return v.iface + " · 自动选择地址"
	}
	return v.iface + " · " + v.sourceIP
}

func (v *nativeView) chooseInterface(name, address string) {
	v.iface, v.sourceIP = name, address
	v.interfacePickerOpen = false
}

func (v *nativeView) interfacePicker(c *ui.Context, locked bool) {
	if locked {
		v.interfacePickerOpen = false
	}
	label := v.interfaceSelectionLabel()
	anchor := ui.Button(c.Key("interface"), label+" ▾").Label("选择出口网卡").Tooltip(label).Height(32).FillWidth().Disabled(locked)
	if anchor.Clicked() {
		v.interfacePickerOpen = !v.interfacePickerOpen
	}
	ui.Popover(c.Key("interface-picker"), anchor, &v.interfacePickerOpen, func() {
		ui.Scroll(c.Key("interface-scroll")).Width(520).Height(280).Children(func() {
			ui.Tree(c.Key("interface-tree"), func() {
				if ui.TreeItem(c.Key("interface-auto"), "自动（系统默认路由）", nil, nil).Selected(v.iface == "").Clicked() {
					v.chooseInterface("", "")
				}
				for _, info := range v.interfaces {
					if !info.IsUp || info.IsLoop {
						continue
					}
					if v.interfaceExpanded == nil {
						v.interfaceExpanded = make(map[string]*bool)
					}
					open := v.interfaceExpanded[info.Name]
					if open == nil {
						open = new(bool)
						*open = true
						v.interfaceExpanded[info.Name] = open
					}
					branch := ui.TreeItem(c.Key("interface-"+info.Name), "网络接口 "+info.Name, open, func() {
						for _, addresses := range [][]string{info.IPv4, info.IPv6} {
							for _, address := range addresses {
								family := "IPv4"
								if strings.Contains(address, ":") {
									family = "IPv6"
								}
								text := family + " · " + address
								if ui.TreeItem(c.Key(info.Name+"-"+address), text, nil, nil).Label(info.Name + " " + text).Tooltip(text).Selected(v.iface == info.Name && v.sourceIP == address).Clicked() {
									v.chooseInterface(info.Name, address)
								}
							}
						}
						if len(info.IPv4)+len(info.IPv6) == 0 {
							ui.Text(c, "无 IP 地址").TextColor(c.Theme().TextMuted).PaddingStart(40)
						}
					})
					if branch.Label("网卡 " + info.Name).Tooltip("选择网卡并自动匹配源地址；展开后可选择具体地址").Selected(v.iface == info.Name && v.sourceIP == "").Clicked() {
						v.chooseInterface(info.Name, "")
					}
				}
			})
		})
	})
}
