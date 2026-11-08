package main

import "tcpconnbenchmark/internal/i18n"

func init() {
	i18n.Register(map[string]string{
		"提示: %s\n": "Notice: %s\n",
		"不启动图形界面，直接在终端运行测试":                                   "Run the test in the terminal without starting the GUI",
		"测试模式: 80, 443, both":                                 "Test mode: 80, 443, both",
		"测试时长（秒）":                                             "Test duration (seconds)",
		"累计失败多少次后停止，0 表示不限制":                                  "Stop after this many failures; 0 means unlimited",
		"每秒发起连接次数":                                            "Connections initiated per second",
		"出口网卡名称，例如 en0；留空使用系统默认路由":                            "Network interface, e.g. en0; leave empty for the default route",
		"源 IPv4 / IPv6 地址，需同时指定 -iface；留空自动匹配":                "Source IPv4 / IPv6 address; requires -iface; leave empty for automatic selection",
		"测试域名，逗号或换行分隔；未指定时读取应用目录配置":                           "Test domains, separated by commas or newlines; defaults to the application configuration",
		"测试结束后导出 CSV 和摘要到应用目录的 logs":                          "Export CSV and summary to the application logs directory after the test",
		"列出可用网卡后退出":                                           "List available network interfaces and exit",
		"读取域名配置失败，使用内置域名: %v\n":                               "Failed to read domain configuration; using built-in domains: %v\n",
		"TcpConn 启动失败: %v\n":                                  "Failed to start TcpConn: %v\n",
		"无效的 -mode %q，可选 80、443、both":                         "Invalid -mode %q; choose 80, 443, or both",
		"启动测试失败: %w":                                          "Failed to start test: %w",
		"清理测试暂存失败: %v\n":                                      "Failed to clean up temporary test data: %v\n",
		"TcpConn · TCP 并发连接测试（CLI）":                           "TcpConn · TCP concurrent connection test (CLI)",
		"模式: %s　出口网卡: %s　域名: %d 个\n":                          "Mode: %s  Interface: %s  Domains: %d\n",
		"时长: %d 秒　失败停止: %s　发起速率: %d 次/秒\n":                    "Duration: %d seconds  Failure limit: %s  Rate: %d connections/s\n",
		"按 Ctrl+C 可提前停止；正在建立连接…":                              "Press Ctrl+C to stop early; establishing connections…",
		"\n正在停止测试…":                                           "\nStopping test…",
		"手动停止":                                                "Stopped manually",
		"[%5.1fs/%3ds] 尝试 %6d　成功 %6d　失败 %5d　进行中 %5d　拨号 %3d\n": "[%5.1fs/%3ds] Attempts %6d  Succeeded %6d  Failed %5d  Active %5d  Dialing %3d\n",
		"测试结束":       "Test finished",
		"停止原因: %s\n": "Stop reason: %s\n",
		"尝试 %d　成功 %d　失败 %d　中止 %d　成功率 %s\n": "Attempts %d  Succeeded %d  Failed %d  Aborted %d  Success rate %s\n",
		"峰值并发: %d　平均发起: %.1f 次/秒\n":        "Peak concurrency: %d  Average rate: %.1f connections/s\n",
		"日志错误: %s": "Log error: %s",
		"日志未导出；可使用 -save 显式保存。": "Logs were not exported; use -save to save them.",
		"获取日志目录失败: %w":          "Failed to locate log directory: %w",
		"导出日志失败: %w":            "Failed to export logs: %w",
		"日志: %s\n摘要: %s\n":      "Log: %s\nSummary: %s\n",
		"不限制":                   "Unlimited",
		"%d 次":                  "%d failures",
	})
}
