package main

import "tcpconnbenchmark/internal/i18n"

func init() {
	i18n.Register(map[string]string{
		"无法确定应用目录: %v":                  "Unable to determine application directory: %v",
		"tcpconnbenchmark · TCP 并发连接测试": "tcpconnbenchmark · TCP concurrent connection test",
		"程序正在关闭":                        "Application is shutting down",
		"当前已有测试正在运行":                    "A test is already running",
		"启动取消":                          "Start canceled",
		"清理上一轮测试暂存失败，保留原会话: %w":         "Failed to clean up the previous test's temporary files; retaining the original session: %w",
		"清理候选测试暂存失败，将在关闭时重试: %w":        "Failed to clean up the candidate test's temporary files; will retry on shutdown: %w",
		"程序关闭":                          "Application shutdown",
		"测试忙碌，无法导出":                     "Test is busy; cannot export",
		"测试会话不存在或已更换":                   "Test session does not exist or has changed",
		"手动停止":                          "Stopped manually",
		"应用目录不可用":                       "Application directory is unavailable",
		"打开日志目录失败: %w":                  "Failed to open log directory: %w",
		"程序正在关闭或测试忙碌，无法保存域名":            "Application is shutting down or test is busy; cannot save domains",
		"应用目录不可用，无法导出日志":                "Application directory is unavailable; cannot export logs",
		"测试尚未结束，无法导出日志":                 "Test has not finished; cannot export logs",
		"清理当前测试暂存失败: %v\n":              "Failed to clean up current test's temporary files: %v\n",
		"清理候选测试暂存失败: %v\n":              "Failed to clean up candidate test's temporary files: %v\n",
		"存在无效域名：%s":                     "Invalid domain: %s",
		"有效域名须为 1–%d 个":                 "There must be 1–%d valid domains",
		"应用目录不可用，无法读取域名配置":              "Application directory is unavailable; cannot read domain configuration",
		"读取域名配置失败: %w":                  "Failed to read domain configuration: %w",
		"域名配置超过大小限制":                    "Domain configuration exceeds the size limit",
		"域名配置格式错误: %w":                  "Invalid domain configuration format: %w",
		"域名配置无效: %w":                    "Invalid domain configuration: %w",
		"应用目录不可用，无法保存域名配置":              "Application directory is unavailable; cannot save domain configuration",
		"内置域名无效: %w":                    "Invalid built-in domains: %w",
		"删除自定义域名配置失败: %w":               "Failed to delete custom domain configuration: %w",
		"编码域名配置失败: %w":                  "Failed to encode domain configuration: %w",
		"保存域名配置失败: %w":                  "Failed to save domain configuration: %w",
	})
}
