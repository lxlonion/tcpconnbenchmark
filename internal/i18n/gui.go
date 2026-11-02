package i18n

func init() {
	Register(map[string]string{
		"域名配置读取失败：": "Failed to read domain configuration: ",
		"请输入有效整数：速率 %d–%d、时长 1–86400、失败停止 0–10000000；域名须为 1–5000 个": "Enter valid integers: rate %d–%d, duration 1–86400, failure limit 0–10000000; 1–5000 domains required",
		"正在启动…": "Starting…", "正在停止…": "Stopping…", "清空视图失败：": "Failed to clear view: ",
		"已清空视图，完整结果未受影响": "View cleared; complete results are unaffected",
		"读取测试状态失败：":      "Failed to read test status: ", "部分实时记录已被覆盖；测试完成后可保存完整结果": "Some live records were overwritten; save complete results after the test",
		"启动失败：": "Failed to start: ", "测试已启动": "Test started", "停止失败：": "Failed to stop: ", "停止请求已完成": "Stop request completed",
		"刷新网卡失败：": "Failed to refresh interfaces: ", "所选网卡或地址已不可用，已切换为自动": "Selected interface or address is unavailable; switched to automatic", "网卡列表已刷新": "Interfaces refreshed",
		"打开日志目录失败：": "Failed to open log directory: ", "已请求打开日志目录": "Requested to open log directory", "保存域名失败：": "Failed to save domains: ",
		"已保存并应用 %d 个域名，忽略 %d 个无效项": "Saved and applied %d domains; ignored %d invalid entries",
		"保存结果失败：": "Failed to save results: ", "结果已保存": "Results saved", "有效域名须为 1–5000 个": "Enter 1–5000 valid domains", "正在保存域名…": "Saving domains…", "正在保存结果…": "Saving results…",
		"TCP 并发连接测试 · 仅握手，不发送 HTTP / TLS 数据": "TCP concurrent connection test · handshake only, no HTTP / TLS data",
		"测试模式": "Test mode", "出口网卡": "Outgoing interface", "刷新网卡": "Refresh interfaces", "发起速率（次/秒）": "Rate (per second)", "测试时长（秒）": "Duration (seconds)", "失败停止（0 不限）": "Failure limit (0: unlimited)",
		"域名 %d 个 · 编辑": "%d domains · Edit", "停止测试": "Stop test", "开始测试": "Start test",
		"测试域名：每行一个，也可粘贴网址；空行和 # 注释会被忽略": "Test domains: one per line; URLs are accepted. Blank lines and # comments are ignored",
		"配置文件：": "Configuration file: ", "有效 %d 个 · 无效 %d 个（最多 5000 个）": "%d valid · %d invalid (maximum 5000)", "无效：": "Invalid: ", "域名草稿": "Domain draft", "恢复内置": "Restore defaults", "清空草稿": "Clear draft", "取消": "Cancel", "保存并应用": "Save and apply", "正在保存…": "Saving…",
		"已运行 %.0f 秒 · 剩余 %.0f 秒": "Elapsed %.0f s · Remaining %.0f s", "测试进度": "Test progress", "失败 %d / %d": "Failures %d / %d", "失败上限进度": "Failure limit progress", "失败上限：不限": "Failure limit: unlimited", "停止原因：": "Stop reason: ", "日志错误：": "Log error: ",
		"实时连接日志": "Live connection log", "全部": "All", "成功": "Success", "失败": "Failure", "中止": "Aborted", "日志筛选": "Log filter", "自动跟随": "Auto-follow", "清空视图": "Clear view", "连接日志": "Connection log",
		"结果尚未保存，开始新测试或关闭后丢失": "Results have not been saved and will be lost when starting a new test or closing", "▸ 日志": "▸ Logs", "▾ 日志": "▾ Logs", "日志": "Logs", "展开或收起日志路径与结果操作": "Expand or collapse log paths and result actions", "打开目录": "Open folder", "保存结果": "Save results", "保存中…": "Saving…",
		"空闲": "Idle", "正在关闭": "Closing", "正在启动": "Starting", "已结束": "Ended", "已终止": "Terminated", "已完成": "Completed", "已停止": "Stopped", "正在停止": "Stopping", "运行中": "Running",
		"尝试": "Attempts", "成功率": "Success rate", "进行中": "Active", "拨号中": "Dialing", "已关闭": "Closed", "峰值": "Peak", "平均速率": "Average rate", " · 次/秒": " · /sec", "指标：": "Metric: ",
		"序号": "No.", "时间": "Time", "状态": "Status", "域名": "Domain", "端口": "Port", "耗时": "Latency", "错误": "Error", "日志状态：": "Log status: ",
		"当前筛选没有匹配记录": "No records match the current filter", "视图已清空，完整结果仍可在测试结束后保存": "View cleared; complete results can still be saved after the test", "等待连接记录…": "Waiting for connection records…", "暂无测试日志，点击「开始测试」": "No test logs yet; click Start test",
		"应用目录": "Application directory", "域名配置路径": "Domain configuration path", "CSV 日志路径": "CSV log path", "摘要文件路径": "Summary file path", "日志目录": "Log directory", "应用": "App", "配置": "Config", "摘要": "Summary", "复制": "Copy",
		"当前平台不支持文件句柄上限调整": "File descriptor limit adjustment is not supported on this platform", "文件句柄软上限 %d；硬上限 %d；目标 %d": "File descriptor soft limit %d; hard limit %d; target %d", " · 调整失败": " · adjustment failed", "文件句柄状态": "File descriptor status", "版本": "Version",
		"自动（系统默认路由）": "Automatic (system default route)", " · 自动选择地址": " · automatic address", "选择出口网卡": "Select outgoing interface", "网络接口 ": "Network interface ", "无 IP 地址": "No IP address", "网卡 ": "Interface ", "选择网卡并自动匹配源地址；展开后可选择具体地址": "Select an interface and automatically match a source address; expand to select an address",
		"达到测试时长": "Test duration reached", "失败次数达到上限": "Failure limit reached", "预先取消": "Cancelled before start", "预先停止": "Stopped before start",
	})
}
