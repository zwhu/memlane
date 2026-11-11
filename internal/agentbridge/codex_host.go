package agentbridge

import (
	"context"
	"errors"
	"os"
	"path/filepath"
)

// The installed path locates Codex. Protocol and workspace/grant checks decide
// usability; version remains diagnostic and an upgrade alone is not a failure.
func checkCodexHostExecutable(ctx context.Context, c Config) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !filepath.IsAbs(c.HostExecutable) {
		return errors.New("Codex executable path unavailable")
	}
	info, err := os.Stat(c.HostExecutable)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return errors.New("Codex executable unavailable; detect its installed location again")
	}
	return nil
}
