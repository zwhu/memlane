//go:build unix

package daemon

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

func TestPlanNoFileTargets(t *testing.T) {
	const inf = uint64(syscall.RLIM_INFINITY)
	isInf := isRLimInfinity

	cases := []struct {
		name       string
		cur, hard  uint64
		target     uint64
		maxPerProc uint64
		want       []uint64
	}{
		{
			name: "darwin infinity capped by target and maxfilesperproc",
			cur:  256, hard: inf, target: 65536, maxPerProc: 138240,
			want: []uint64{65536, 16384, 10240, 4096},
		},
		{
			name: "darwin maxfilesperproc lower than target",
			cur:  256, hard: inf, target: 65536, maxPerProc: 12288,
			want: []uint64{12288, 10240, 4096},
		},
		{
			name: "finite hard limit caps target",
			cur:  256, hard: 8192, target: 65536, maxPerProc: 138240,
			want: []uint64{8192, 4096},
		},
		{
			name: "finite hard limit below 4096 still raises to hard",
			cur:  256, hard: 1024, target: 65536, maxPerProc: 138240,
			want: []uint64{1024},
		},
		{
			name: "already higher soft limit never downgrades",
			cur:  138240, hard: inf, target: 65536, maxPerProc: 138240,
			want: nil,
		},
		{
			name: "already at hard limit produces no targets",
			cur:  256, hard: 256, target: 65536, maxPerProc: 138240,
			want: nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := planNoFileTargets(tc.cur, tc.hard, tc.target, tc.maxPerProc, isInf)
			if !slices.Equal(got, tc.want) {
				t.Fatalf("planNoFileTargets(%d, %d, %d, %d) = %v, want %v",
					tc.cur, tc.hard, tc.target, tc.maxPerProc, got, tc.want)
			}
		})
	}
}

func TestRaiseNoFileLimitWithStepDownAndNoDowngrade(t *testing.T) {
	const inf = uint64(syscall.RLIM_INFINITY)

	t.Run("steps down on EINVAL until accepted", func(t *testing.T) {
		state := rlimitPair{Cur: 256, Max: inf}
		var tried []uint64
		cur, err := raiseNoFileLimitWith(
			DefaultTargetNoFileLimit,
			138240,
			isRLimInfinity,
			func() (rlimitPair, error) { return state, nil },
			func(p rlimitPair) error {
				tried = append(tried, p.Cur)
				if p.Cur > 10240 {
					return syscall.EINVAL
				}
				state = p
				return nil
			},
			true,
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cur != 10240 || state.Max != inf {
			t.Fatalf("cur=%d state=%+v, want cur=10240 max=inf", cur, state)
		}
		wantTried := []uint64{65536, 16384, 10240}
		if !slices.Equal(tried, wantTried) {
			t.Fatalf("tried = %v, want %v", tried, wantTried)
		}
	})

	t.Run("preserves higher soft limit and commits once for child inheritance", func(t *testing.T) {
		state := rlimitPair{Cur: 138240, Max: inf}
		var commits []rlimitPair
		cur, err := raiseNoFileLimitWith(
			DefaultTargetNoFileLimit,
			138240,
			isRLimInfinity,
			func() (rlimitPair, error) { return state, nil },
			func(p rlimitPair) error {
				commits = append(commits, p)
				state = p
				return nil
			},
			true,
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cur != 138240 || len(commits) != 1 || commits[0] != (rlimitPair{Cur: 138240, Max: inf}) {
			t.Fatalf("cur=%d commits=%+v, want single preserve commit at 138240", cur, commits)
		}
	})

	t.Run("returns last error if all candidates fail", func(t *testing.T) {
		state := rlimitPair{Cur: 256, Max: inf}
		cur, err := raiseNoFileLimitWith(
			DefaultTargetNoFileLimit,
			138240,
			isRLimInfinity,
			func() (rlimitPair, error) { return state, nil },
			func(rlimitPair) error { return syscall.EPERM },
			true,
		)
		if !errors.Is(err, syscall.EPERM) || cur != 256 {
			t.Fatalf("cur=%d err=%v, want 256 / EPERM", cur, err)
		}
	})
}

func TestEnsureFileDescriptorLimitRealOS(t *testing.T) {
	if runtime.GOOS == "darwin" {
		if n := maxFilesPerProc(); n < 256 {
			t.Fatalf("darwin kern.maxfilesperproc = %d, want >= 256", n)
		}
	}

	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	cmd := exec.Command("/bin/sh", "-c", `ulimit -Sn 256; exec "$@"`, "sh", exe, "-test.run=^TestEnsureFileDescriptorLimitSubprocessHelper$", "-test.v")
	cmd.Env = append(os.Environ(), "MEMLANE_TEST_RLIMIT_HELPER=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("real OS rlimit helper failed: %v\n%s", err, string(out))
	}
}

func TestEnsureFileDescriptorLimitSubprocessHelper(t *testing.T) {
	if os.Getenv("MEMLANE_TEST_RLIMIT_HELPER") != "1" {
		return
	}

	readChildNoFile := func() uint64 {
		t.Helper()
		out, err := exec.Command("/bin/sh", "-c", "ulimit -Sn").Output()
		if err != nil {
			t.Fatalf("child ulimit -Sn: %v", err)
		}
		raw := strings.TrimSpace(string(out))
		if raw == "unlimited" {
			return uint64(syscall.RLIM_INFINITY)
		}
		val, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			t.Fatalf("parse child ulimit %q: %v", raw, err)
		}
		return val
	}

	// 1. Fresh Go process started under ulimit -Sn 256: EnsureFileDescriptorLimit
	// must commit the raised limit so spawned children inherit >= 4096 instead of 256.
	cur, err := EnsureFileDescriptorLimit(DefaultTargetNoFileLimit)
	if err != nil {
		t.Fatalf("EnsureFileDescriptorLimit (initial): %v", err)
	}
	if cur < 4096 {
		t.Fatalf("initial raised soft limit = %d, want >= 4096", cur)
	}
	if childLim := readChildNoFile(); childLim < 4096 {
		t.Fatalf("grandchild inherited soft limit = %d after EnsureFileDescriptorLimit, want >= 4096", childLim)
	}

	// 2. Explicitly lower current soft limit back to 256, then verify
	// EnsureFileDescriptorLimit raises it on the live kernel (without EINVAL)
	// and propagates it to children.
	var lim syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &lim); err != nil {
		t.Fatalf("Getrlimit: %v", err)
	}
	low := syscall.Rlimit{Cur: 256, Max: lim.Max}
	if err := syscall.Setrlimit(syscall.RLIMIT_NOFILE, &low); err != nil {
		t.Fatalf("lower soft limit to 256: %v", err)
	}
	raised, err := EnsureFileDescriptorLimit(DefaultTargetNoFileLimit)
	if err != nil {
		t.Fatalf("EnsureFileDescriptorLimit (from 256): %v", err)
	}
	var after syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &after); err != nil {
		t.Fatalf("Getrlimit after raise: %v", err)
	}
	if raised < 4096 || after.Cur != raised {
		t.Fatalf("raised=%d after.Cur=%d, want >= 4096 and matching", raised, after.Cur)
	}
	if childLim := readChildNoFile(); childLim != raised {
		t.Fatalf("grandchild soft limit = %d, want %d (parent raised limit)", childLim, raised)
	}
	fmt.Printf("verified real OS rlimit raise: 256 -> %d (child=%d)\n", raised, readChildNoFile())
}
