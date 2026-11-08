package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"tcpconnbenchmark/internal/appdir"
	"tcpconnbenchmark/internal/i18n"
	"tcpconnbenchmark/internal/syslimit"
	"tcpconnbenchmark/internal/tester"
)

func main() {
	fdInfo := syslimit.Raise(tester.FDSoftLimitTarget)
	if fdInfo.Error != "" {
		fmt.Fprintf(os.Stderr, i18n.T("提示: %s\n"), fdInfo.Error)
	}

	cliMode := flag.Bool("cli", false, i18n.T("不启动图形界面，直接在终端运行测试"))
	modeFlag := flag.String("mode", "both", i18n.T("测试模式: 80, 443, both"))
	duration := flag.Int("duration", tester.DefaultDurationSec, i18n.T("测试时长（秒）"))
	failStop := flag.Int("fail-stop", tester.DefaultFailStop, i18n.T("累计失败多少次后停止，0 表示不限制"))
	rate := flag.Int("rate", tester.DefaultRatePerSec, i18n.T("每秒发起连接次数"))
	iface := flag.String("iface", "", i18n.T("出口网卡名称，例如 en0；留空使用系统默认路由"))
	sourceIP := flag.String("source-ip", "", i18n.T("源 IPv4 / IPv6 地址，需同时指定 -iface；留空自动匹配"))
	domainsFlag := flag.String("domains", "", i18n.T("测试域名，逗号或换行分隔；未指定时读取应用目录配置"))
	save := flag.Bool("save", false, i18n.T("测试结束后导出 CSV 和摘要到应用目录的 logs"))
	listInterfaces := flag.Bool("list-interfaces", false, i18n.T("列出可用网卡后退出"))
	flag.Parse()

	if *listInterfaces {
		for _, info := range tester.ListInterfaces() {
			addr, addr6 := "-", "-"
			if len(info.IPv4) > 0 {
				addr = strings.Join(info.IPv4, ", ")
			}
			if len(info.IPv6) > 0 {
				addr6 = strings.Join(info.IPv6, ", ")
			}
			fmt.Printf("%-12s index=%-3d ipv4=%s ipv6=%s\n", info.Name, info.Index, addr, addr6)
		}
		return
	}

	if *cliMode {
		mode, err := parseCLIMode(*modeFlag)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		domains := append([]string(nil), tester.BuiltinDomains...)
		domainsSpecified := false
		flag.Visit(func(f *flag.Flag) {
			if f.Name == "domains" {
				domainsSpecified = true
			}
		})
		if domainsSpecified {
			domains = []string{*domainsFlag}
		} else if dir, err := appdir.Directory(); err != nil {
			fmt.Fprintf(os.Stderr, i18n.T("读取域名配置失败，使用内置域名: %v\n"), err)
		} else {
			var err error
			domains, err = loadDomainConfig(dir, domains)
			if err != nil {
				fmt.Fprintf(os.Stderr, i18n.T("读取域名配置失败，使用内置域名: %v\n"), err)
			}
		}
		if err := runCLI(tester.Config{
			Mode:        mode,
			Interface:   *iface,
			SourceIP:    *sourceIP,
			Domains:     domains,
			DurationSec: *duration,
			FailStop:    *failStop,
			RatePerSec:  *rate,
		}, *save); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	app := newApp(fdInfo)
	if err := app.run(); err != nil {
		fmt.Fprintf(os.Stderr, i18n.T("tcpconnbenchmark 启动失败: %v\n"), err)
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
		return "", fmt.Errorf(i18n.T("无效的 -mode %q，可选 80、443、both"), value)
	}
}
