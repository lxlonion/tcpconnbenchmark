//go:build !darwin && !linux && !freebsd && !netbsd && !openbsd && !dragonfly && !windows

package syslimit

// Info describes the process file descriptor limit before and after the
// startup adjustment.
type Info struct {
	Target    uint64 `json:"target"`
	Soft      uint64 `json:"soft"`
	Hard      uint64 `json:"hard"`
	Raised    bool   `json:"raised"`
	Supported bool   `json:"supported"`
	Error     string `json:"error,omitempty"`
}

// Raise is a no-op on platforms without a supported rlimit implementation.
func Raise(target uint64) Info {
	return Info{Target: target, Supported: false}
}
