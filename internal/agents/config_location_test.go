package agents

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

func TestApplyPiScopesAcceptsDirectoryAliasAndRejectsMove(t *testing.T) {
	root := t.TempDir()
	program := filepath.Join(root, "pi")
	if err := os.WriteFile(program, []byte("#!/bin/sh\nif test \"$1\" = --help; then printf '%s\\n' --extension; else printf '0.84.1\\n'; fi\n"), 0700); err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(root, "settings")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(directory, alias); err != nil {
		t.Fatal(err)
	}
	c := records.Connection{ID: uuid.NewString(), Agent: "pi", Generation: 1, Enabled: true, AllowedScopes: []string{}}
	o := InstallOptions{ClientRoot: filepath.Join(root, "client"), ServiceRoot: filepath.Join(root, "service"), Bridge: program, AgentPath: program, PiDirectory: directory}
	if _, err := Install(context.Background(), c, o); err != nil {
		t.Fatal(err)
	}
	c.Generation = 2
	c.AllowedScopes = []string{uuid.NewString()}
	o.PiDirectory = alias
	if _, err := Install(context.Background(), c, o); err != nil {
		t.Fatal("same directory alias rejected during scope application", err)
	}
	before, err := os.ReadFile(filepath.Join(directory, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(root, "other")
	if err = os.Mkdir(moved, 0700); err != nil {
		t.Fatal(err)
	}
	o.PiDirectory = moved
	c.Generation = 3
	if _, err = Install(context.Background(), c, o); err == nil || !strings.Contains(err.Error(), "disconnect before") {
		t.Fatal("configuration move accepted", err)
	}
	after, err := os.ReadFile(filepath.Join(directory, "settings.json"))
	if err != nil || string(before) != string(after) {
		t.Fatal("old configuration changed after rejected move", err)
	}
	if _, err = os.Stat(filepath.Join(moved, "settings.json")); !os.IsNotExist(err) {
		t.Fatal("new directory changed after rejected move", err)
	}
}
