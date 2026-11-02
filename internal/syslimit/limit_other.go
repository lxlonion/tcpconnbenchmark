//go:build !darwin && !linux && !freebsd && !netbsd && !openbsd && !dragonfly && !windows

package syslimit

// Raise is a no-op on platforms without a supported rlimit implementation.
func Raise(target uint64) Info {
	return Info{Target: target, Supported: false}
}
