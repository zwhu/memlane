package supervisor

import (
	"context"
	"syscall"
	"testing"
	"time"
)

func TestTrackAndStopDescendantsProtectsAgainstPIDReuse(t *testing.T) {
	known := map[int]string{
		7001: "Sat Nov 21 10:00:00 2026", // still owned under parent 500
		7002: "Sat Nov 21 10:00:00 2026", // reparented to PID 1 (detached setsid grandchild)
		7003: "Sat Nov 21 09:00:00 2026", // exited and PID reused with newer lstart!
	}
	table := map[int]ProcessInfo{
		500:  {PID: 500, PPID: 1, PGID: 500, Started: "Sat Nov 21 09:59:00 2026"},
		7001: {PID: 7001, PPID: 500, PGID: 7001, Started: "Sat Nov 21 10:00:00 2026"},
		7002: {PID: 7002, PPID: 1, PGID: 7002, Started: "Sat Nov 21 10:00:00 2026"},
		7003: {PID: 7003, PPID: 1, PGID: 7003, Started: "Sat Nov 21 11:30:00 2026"}, // reused!
		7004: {PID: 7004, PPID: 7001, PGID: 7004, Started: "Sat Nov 21 10:05:00 2026"},
	}

	changed := trackDescendantsFromTable([]int{500}, known, table)
	if !changed {
		t.Fatal("expected known map to change after adding 7004 and pruning reused 7003")
	}
	if _, reusedKept := known[7003]; reusedKept {
		t.Fatal("expected reused PID 7003 with mismatched lstart to be pruned")
	}
	if known[7002] != "Sat Nov 21 10:00:00 2026" {
		t.Fatalf("expected detached reparented descendant 7002 preserved, got %q", known[7002])
	}
	if known[7004] != "Sat Nov 21 10:05:00 2026" {
		t.Fatalf("expected nested descendant 7004 captured, got %q", known[7004])
	}

	// Verify stopTrackedDescendantsWith never signals a reused PID even if caller passes it.
	signaled := make(map[int][]syscall.Signal)
	targetKnown := map[int]string{
		7001: "Sat Nov 21 10:00:00 2026",
		7003: "Sat Nov 21 09:00:00 2026", // stale lstart; table holds newer 11:30:00
	}
	live7001 := true
	err := stopTrackedDescendantsWith(
		context.Background(),
		targetKnown,
		150*time.Millisecond,
		150*time.Millisecond,
		func(context.Context) (map[int]ProcessInfo, error) {
			out := map[int]ProcessInfo{
				7003: {PID: 7003, PPID: 1, PGID: 7003, Started: "Sat Nov 21 11:30:00 2026"},
			}
			if live7001 {
				out[7001] = ProcessInfo{PID: 7001, PPID: 500, PGID: 7001, Started: "Sat Nov 21 10:00:00 2026"}
			}
			return out, nil
		},
		func(pid int, sig syscall.Signal) error {
			signaled[pid] = append(signaled[pid], sig)
			if pid == 7001 && sig == syscall.SIGKILL {
				live7001 = false
			}
			return nil
		},
	)
	if err != nil {
		t.Fatalf("stopTrackedDescendantsWith failed: %v", err)
	}
	if len(signaled[7003]) != 0 {
		t.Fatalf("PID reuse guard failed: signaled mismatched PID 7003 with %v", signaled[7003])
	}
	if len(signaled[7001]) != 2 || signaled[7001][0] != syscall.SIGTERM || signaled[7001][1] != syscall.SIGKILL {
		t.Fatalf("expected SIGTERM then SIGKILL on 7001, got %v", signaled[7001])
	}
}
