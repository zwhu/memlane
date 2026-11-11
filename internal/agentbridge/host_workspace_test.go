package agentbridge

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestHostWorkspaceOnlyAcceptsPrivateStdioBoundary(t *testing.T) {
	base := Config{Root: "/generated/service", ConnectionID: "generated", Generation: 1, HostWorkspace: "codex-stdio-v1", HostExecutable: "/generated/codex", HostVersion: "codex-cli 0.167.0"}
	if err := base.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*Config){func(c *Config) { c.HostWorkspace = "cwd" }, func(c *Config) { c.Workspace = "/generated/fixed" }, func(c *Config) { c.ToolMode = "initialization" }} {
		c := base
		change(&c)
		if c.Validate() == nil {
			t.Fatal("untrusted/fixed context accepted", c)
		}
	}
}

func TestCodexHostUsesExecutablePathRatherThanVersion(t *testing.T) {
	root := t.TempDir()
	program := filepath.Join(root, "codex")
	write := func(version string) {
		t.Helper()
		if err := os.WriteFile(program, []byte("#!/bin/sh\ntest \"$1\" = --version || exit 4\ntest \"$HOME\" != \""+root+"\" || exit 5\nprintf '%s\\n' '"+version+"'\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	c := Config{HostExecutable: program, HostVersion: "codex-cli 0.167.0"}
	write(c.HostVersion)
	if err := checkCodexHostExecutable(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	write("codex-cli 0.168.0")
	if err := checkCodexHostExecutable(context.Background(), c); err != nil {
		t.Fatal("in-place upgrade rejected despite available executable", err)
	}
	if err := os.Chmod(program, 0600); err != nil {
		t.Fatal(err)
	}
	if checkCodexHostExecutable(context.Background(), c) == nil {
		t.Fatal("unexecutable path accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if checkCodexHostExecutable(ctx, c) == nil {
		t.Fatal("cancelled host probe accepted")
	}
}
