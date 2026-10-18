package main

import (
	"flag"
	"fmt"
	"os"
	"runtime"
	"strings"

	"tcplink/internal/syslimit"
	"tcplink/internal/tester"
)

func main() {
	// The webview backend must run on the main OS thread, especially on
	// macOS.
	runtime.LockOSThread()

	fdInfo := syslimit.Raise(tester.FDSoftLimitTarget)
	if fdInfo.Error != "" {
		fmt.Fprintf(os.Stderr, "提示: %s\n", fdInfo.Error)
	}

	cliMode := flag.Bool("cli", false, "不启动图形界面，直接在终端运行测试")
	modeFlag := flag.String("mode", "both", "测试模式: 80, 443, both")
	duration := flag.Int("duration", 10, "测试时长（秒）")
	failStop := flag.Int("fail-stop", 50, "累计失败多少次后停止，0 表示不限制")
	rate := flag.Int("rate", tester.DefaultRatePerSec, "每秒发起连接次数")
	iface := flag.String("iface", "", "出口网卡名称，例如 en0；留空使用系统默认路由")
	domainsFlag := flag.String("domains", "", "测试域名，逗号或换行分隔；留空使用内置 84 个域名")
	listInterfaces := flag.Bool("list-interfaces", false, "列出可用网卡后退出")
	flag.Parse()

	if *listInterfaces {
		for _, info := range tester.ListInterfaces() {
			addr := "-"
			if len(info.IPv4) > 0 {
				addr = strings.Join(info.IPv4, ", ")
			}
			fmt.Printf("%-12s index=%-3d ipv4=%s\n", info.Name, info.Index, addr)
		}
		return
	}

	if *cliMode {
		mode, err := parseCLIMode(*modeFlag)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		domains := tester.BuiltinDomains
		if strings.TrimSpace(*domainsFlag) != "" {
			domains = []string{*domainsFlag}
		}
		runCLI(tester.Config{
			Mode:        mode,
			Interface:   *iface,
			Domains:     domains,
			DurationSec: *duration,
			FailStop:    *failStop,
			RatePerSec:  *rate,
		})
		return
	}

	app := newApp(fdInfo)
	if err := app.run(); err != nil {
		fmt.Fprintf(os.Stderr, "TCPLink 启动失败: %v\n", err)
		os.Exit(1)
	}
}

func parseCLIMode(value string) (tester.Mode, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "80", "http":
		return tester.Mode80, nil
	case "443", "https":
		return tester.Mode443, nil
	case "both", "80+443", "80,443", "all":
		return tester.ModeBoth, nil
	default:
		return "", fmt.Errorf("无效的 -mode %q，可选 80、443、both", value)
	}
}
