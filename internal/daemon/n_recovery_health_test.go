package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNRecoveryHealthReportsActualSupervisorWindow(t *testing.T) {
	s := &Server{status: "failed", state: State{Restarts: []time.Time{time.Now(), time.Now().Add(-time.Minute), time.Now().Add(-2 * time.Minute), time.Now().Add(-11 * time.Minute)}}}
	out, err := s.perform(context.Background(), Request{Op: "health"})
	if err != nil {
		t.Fatal(err)
	}
	m := out.(map[string]any)
	if m["recovery"] != "exhausted" || m["recovery_attempts"] != 3 || m["recovery_limit"] != 3 || m["recovery_window_seconds"] != 600 {
		t.Fatal(m)
	}
	s.status = "recovering"
	out, err = s.perform(context.Background(), Request{Op: "health"})
	if err != nil {
		t.Fatal(err)
	}
	if out.(map[string]any)["recovery"] != "recovering" {
		t.Fatal("recovery status invented")
	}
	s.status = "unavailable"
	out, err = s.perform(context.Background(), Request{Op: "health"})
	if err != nil {
		t.Fatal(err)
	}
	if out.(map[string]any)["recovery"] != "" {
		t.Fatal("generic unavailable became exhausted")
	}
}

func TestNRecoveryRetryReceiptIdentifiesNewCycleBeforeReady(t *testing.T) {
	s := &Server{config: Config{Root: t.TempDir()}, status: "failed", wake: make(chan struct{}, 1), state: State{Restarts: []time.Time{time.Now(), time.Now(), time.Now()}}}
	before, err := s.perform(context.Background(), Request{Op: "health"})
	if err != nil {
		t.Fatal(err)
	}
	old := before.(map[string]any)
	ack, err := s.perform(context.Background(), Request{Op: "retry", UserConfirmed: true})
	if err != nil {
		t.Fatal(err)
	}
	receipt := ack.(map[string]any)
	if receipt["retry_requested"] != true || receipt["recovery_owner"] != old["recovery_owner"] || receipt["recovery_generation"] != int64(1) || s.status == "ready" || s.worker != nil {
		t.Fatal("retry ACK invented ready or failed to identify cycle", receipt)
	}
	after, err := s.perform(context.Background(), Request{Op: "health"})
	if err != nil {
		t.Fatal(err)
	}
	if m := after.(map[string]any); m["state"] != "recovering" || m["recovery_generation"] != int64(1) || m["recovery_attempts"] != 0 {
		t.Fatal("health did not expose actual reset cycle", m)
	}
}

func TestNRecoveryFailedDurableResetPreservesCeilingAndDoesNotWake(t *testing.T) {
	root := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(root, []byte("owned fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	s := &Server{config: Config{Root: root}, status: "failed", wake: make(chan struct{}, 1), state: State{Restarts: []time.Time{time.Now(), time.Now(), time.Now()}}}
	if _, err := s.perform(context.Background(), Request{Op: "retry", UserConfirmed: true}); err == nil {
		t.Fatal("failed persistence became accepted reset")
	}
	if len(s.state.Restarts) != 3 || s.recoveryGeneration != 0 || s.status != "failed" || len(s.wake) != 0 {
		t.Fatal("failed reset changed the actual recovery ceiling")
	}
}
