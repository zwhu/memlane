package agentbridge

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type hostVersionOutput struct{ text strings.Builder }

func (o *hostVersionOutput) Write(data []byte) (int, error) {
	if o.text.Len()+len(data) > 1024 {
		return 0, errors.New("host version output exceeded limit")
	}
	return o.text.Write(data)
}

// A previously installed profile fails closed after an in-place CLI upgrade.
// The explicit discovered executable is checked in a new empty configuration,
// never the user's login/configuration and never by launching a model.
func checkCodexHostVersion(ctx context.Context, c Config) error {
	root, err := os.MkdirTemp("", "memlane-codex-host-version-")
	if err != nil {
		return errors.New("Codex host version unavailable")
	}
	defer os.RemoveAll(root)
	run, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	command := exec.CommandContext(run, c.HostExecutable, "--version")
	command.Dir = root
	command.Env = []string{"HOME=" + root, "CODEX_HOME=" + filepath.Join(root, "empty-codex"), "PATH=" + filepath.Dir(c.HostExecutable) + ":/usr/bin:/bin", "TMPDIR=" + root}
	var out hostVersionOutput
	command.Stdout = &out
	command.WaitDelay = time.Second
	if command.Run() != nil || strings.TrimSpace(out.text.String()) != c.HostVersion {
		return errors.New("Codex host version changed; reconnect using a supported version")
	}
	return nil
}
