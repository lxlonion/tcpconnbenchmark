//go:build darwin || linux || freebsd || netbsd || openbsd || dragonfly

package syslimit

import (
	"fmt"
	"syscall"
)

// Raise tries to lift the soft file descriptor limit to target without ever
// exceeding the system hard limit. A soft limit that is already higher is left
// untouched.
func Raise(target uint64) Info {
	info := Info{Target: target, Supported: true}

	var lim syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &lim); err != nil {
		info.Error = fmt.Sprintf("读取文件句柄上限失败: %v", err)
		return info
	}
	info.Soft = uint64(lim.Cur)
	info.Hard = uint64(lim.Max)

	desired := target
	if desired > uint64(lim.Max) {
		desired = uint64(lim.Max)
	}
	if uint64(lim.Cur) >= desired {
		return info
	}

	lim.Cur = desired
	if err := syscall.Setrlimit(syscall.RLIMIT_NOFILE, &lim); err != nil {
		info.Error = fmt.Sprintf("提升文件句柄上限失败: %v", err)
		return info
	}
	info.Raised = true

	var updated syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &updated); err == nil {
		info.Soft = uint64(updated.Cur)
		info.Hard = uint64(updated.Max)
	} else {
		info.Soft = desired
	}
	return info
}
