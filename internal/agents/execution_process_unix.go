//go:build darwin || linux

package agents

import (
	"os"
	"os/exec"
	"syscall"
	"time"
)

// Verification children have their own process group. Cancelling a CLI closes
// its MCP descendants as well, including descendants holding a stdout pipe.
func configureAgentProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if err == syscall.ESRCH {
			return os.ErrProcessDone
		}
		return err
	}
	cmd.WaitDelay = time.Second
}
func finishAgentProcess(cmd *exec.Cmd) bool {
	if cmd.Process == nil {
		return true
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	deadline := time.Now().Add(time.Second)
	for {
		if syscall.Kill(-cmd.Process.Pid, 0) == syscall.ESRCH {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
}
