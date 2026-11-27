//go:build unix

package daemon

import (
	"sync/atomic"
	"syscall"
)

var nofileLimitCommitted atomic.Bool

func isRLimInfinity(v uint64) bool {
	return v == uint64(syscall.RLIM_INFINITY) || v == ^uint64(0)
}

// EnsureFileDescriptorLimit raises RLIMIT_NOFILE soft limit up to target (bounded
// by kern.maxfilesperproc on macOS and the process hard limit) without ever
// lowering an already larger soft limit. The first successful call always invokes
// syscall.Setrlimit so Go clears origRlimitNofile and spawned children inherit the
// raised limit instead of macOS's default 256.
func EnsureFileDescriptorLimit(target uint64) (uint64, error) {
	if target == 0 {
		target = DefaultTargetNoFileLimit
	}
	getFn := func() (rlimitPair, error) {
		var lim syscall.Rlimit
		if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &lim); err != nil {
			return rlimitPair{}, err
		}
		return rlimitPair{Cur: lim.Cur, Max: lim.Max}, nil
	}
	setFn := func(p rlimitPair) error {
		lim := syscall.Rlimit{Cur: p.Cur, Max: p.Max}
		return syscall.Setrlimit(syscall.RLIMIT_NOFILE, &lim)
	}
	cur, err := raiseNoFileLimitWith(target, maxFilesPerProc(), isRLimInfinity, getFn, setFn, !nofileLimitCommitted.Load())
	if err == nil {
		nofileLimitCommitted.Store(true)
	}
	return cur, err
}
