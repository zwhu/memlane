//go:build darwin || linux

package agents

import (
	"bufio"
	"context"
	"os/exec"
	"strconv"
	"syscall"
	"testing"
	"time"
)

// This is an explicit OS process fault fixture, not a replacement for a real
// Agent's normal or cancelled model run. The descendant keeps stdout open.
func TestVerificationCancellationClosesDescendantHeldPipe(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", `sleep 30 & printf '%s\n' "$!"; wait`)
	configureAgentProcess(cmd)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer finishAgentProcess(cmd)
	scan := bufio.NewScanner(stdout)
	if !scan.Scan() {
		t.Fatal("descendant PID missing", scan.Err())
	}
	child, err := strconv.Atoi(scan.Text())
	if err != nil || child <= 0 {
		t.Fatal("invalid descendant PID", scan.Text())
	}
	if err = syscall.Kill(child, 0); err != nil {
		t.Fatal("fixture descendant was not active", err)
	}
	started := time.Now()
	cancel()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err = <-done:
		if err == nil {
			t.Fatal("cancelled fixture returned success")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled descendant held the pipe open")
	}
	if !finishAgentProcess(cmd) {
		t.Fatal("verification process group survived cancellation")
	}
	if time.Since(started) > 3*time.Second {
		t.Fatal("cancellation exceeded bounded exit")
	}
	if syscall.Kill(child, 0) != syscall.ESRCH {
		t.Fatal("verification descendant survived cancellation")
	}
}
