//go:build darwin || linux

package agents

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

// RunProposalGuard is an internal helper mode. It owns no service config or
// database. The Agent inherits this isolated process group, not the control FD.
func RunProposalGuard(args []string) int {
	if len(args) == 0 || !filepath.IsAbs(args[0]) || os.Getpid() <= 1 || syscall.Getpgrp() != os.Getpid() {
		return 2
	}
	control := os.NewFile(3, "proposal-owner-control")
	if control == nil {
		return 2
	}
	defer control.Close()
	info, err := control.Stat()
	if err != nil || info.Mode()&os.ModeNamedPipe == 0 {
		return 2
	}
	syscall.CloseOnExec(3)
	var permit [1]byte
	if _, err = io.ReadFull(control, permit[:]); err != nil || permit[0] != 'S' {
		return 2
	}
	done := make(chan struct{})
	defer close(done)
	go func() {
		_, _ = io.Copy(io.Discard, control)
		select {
		case <-done:
			return
		default:
		}
		// EOF or control failure means the service owner disappeared. Killing
		// this group also terminates the guard, so no late model continues.
		_ = syscall.Kill(-os.Getpid(), syscall.SIGKILL)
	}()
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err = cmd.Run(); err != nil {
		if cmd.ProcessState != nil && cmd.ProcessState.ExitCode() > 0 {
			return cmd.ProcessState.ExitCode()
		}
		return 1
	}
	return 0
}
