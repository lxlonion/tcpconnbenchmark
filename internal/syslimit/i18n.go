package syslimit

import "tcpconnbenchmark/internal/i18n"

func init() {
	i18n.Register(map[string]string{
		"读取文件句柄上限失败: %v": "Failed to read file descriptor limit: %v",
		"提升文件句柄上限失败: %v": "Failed to raise file descriptor limit: %v",
	})
}
