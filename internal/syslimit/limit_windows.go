//go:build windows

package syslimit

// Raise is a no-op on Windows, which does not expose an RLIMIT_NOFILE style
// limit. The process still reports the requested target so the UI can explain
// what was attempted.
func Raise(target uint64) Info {
	return Info{Target: target, Supported: false}
}
