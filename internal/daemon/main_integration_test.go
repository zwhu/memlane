package daemon_test

import (
	"os"
	"testing"

	"github.com/zwhu/memlane/internal/agents"
)

// Real in-process service acceptance uses its own test executable as the
// self-contained guard, exactly as the packaged memlaned dispatch does.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "--proposal-guard" {
		os.Exit(agents.RunProposalGuard(os.Args[2:]))
	}
	os.Exit(m.Run())
}
