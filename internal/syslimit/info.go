package syslimit

// Info describes the process file descriptor limit before and after the
// startup adjustment.
type Info struct {
	Target    uint64
	Soft      uint64
	Hard      uint64
	Raised    bool
	Supported bool
	Error     string
}
