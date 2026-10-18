//go:build windows

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

// Raise is a no-op on Windows, which does not expose an RLIMIT_NOFILE style
// limit. The process still reports the requested target so the UI can explain
// what was attempted.
func Raise(target uint64) Info {
	return Info{Target: target, Supported: false}
}
