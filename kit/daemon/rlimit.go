package daemon

import "slices"

// DefaultTargetNoFileLimit is the preferred soft RLIMIT_NOFILE target for the
// daemon and its managed child processes (such as the Cognee/LanceDB runner).
const DefaultTargetNoFileLimit uint64 = 65536

var nofileTierTargets = []uint64{16384, 10240, 4096}

type rlimitPair struct {
	Cur uint64
	Max uint64
}

// planNoFileTargets returns descending candidate soft limits strictly greater
// than cur, bounded by hard (when finite) and maxPerProc (when > 0).
// It never proposes a value lower than cur.
func planNoFileTargets(cur, hard, target, maxPerProc uint64, isInf func(uint64) bool) []uint64 {
	if isInf != nil && isInf(cur) {
		return nil
	}
	if target == 0 {
		target = DefaultTargetNoFileLimit
	}
	var ceiling uint64
	hasCeiling := false
	if hard > 0 && (isInf == nil || !isInf(hard)) {
		ceiling = hard
		hasCeiling = true
	}
	if maxPerProc > 0 && (!hasCeiling || maxPerProc < ceiling) {
		ceiling = maxPerProc
		hasCeiling = true
	}

	raw := make([]uint64, 0, len(nofileTierTargets)+1)
	raw = append(raw, target)
	raw = append(raw, nofileTierTargets...)

	out := make([]uint64, 0, len(raw))
	for _, candidate := range raw {
		if hasCeiling && candidate > ceiling {
			candidate = ceiling
		}
		if candidate <= cur {
			continue
		}
		if !slices.Contains(out, candidate) {
			out = append(out, candidate)
		}
	}
	slices.SortFunc(out, func(a, b uint64) int {
		switch {
		case a > b:
			return -1
		case a < b:
			return 1
		default:
			return 0
		}
	})
	return out
}

func raiseNoFileLimitWith(
	target uint64,
	maxPerProc uint64,
	isInf func(uint64) bool,
	getFn func() (rlimitPair, error),
	setFn func(rlimitPair) error,
	forceCommit bool,
) (uint64, error) {
	current, err := getFn()
	if err != nil {
		return 0, err
	}
	candidates := planNoFileTargets(current.Cur, current.Max, target, maxPerProc, isInf)
	if len(candidates) == 0 {
		if forceCommit {
			// Calling Setrlimit(RLIMIT_NOFILE, ...) explicitly clears Go's
			// saved origRlimitNofile so child processes inherit the raised limit
			// instead of being reset to macOS's 256 default by forkExec.
			if err := setFn(current); err != nil {
				return current.Cur, err
			}
		}
		return current.Cur, nil
	}

	var lastErr error
	for _, candidate := range candidates {
		if err := setFn(rlimitPair{Cur: candidate, Max: current.Max}); err == nil {
			if after, readErr := getFn(); readErr == nil {
				return after.Cur, nil
			}
			return candidate, nil
		} else {
			lastErr = err
		}
	}
	return current.Cur, lastErr
}
