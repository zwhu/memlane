//go:build darwin || linux

package agents

import (
	"context"
	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Exact F9 call site: gen2 scoped protocol proof, no legacy read/write proof.
// The executable answers only local metadata probes, never model requests.
func TestPiProjectCurrentScopedProtocolWithoutLegacyReadWrite(t *testing.T) {
	root := t.TempDir()
	program := filepath.Join(root, "pi")
	if err := os.WriteFile(program, []byte("#!/bin/sh\ncase \"$1\" in --version) echo 0.84.1;; --help) echo --extension;; *) exit 79;; esac\n"), 0700); err != nil {
		t.Fatal(err)
	}
	profile := filepath.Join(root, "profile")
	if err := os.Mkdir(profile, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profile, "settings.json"), []byte(`{"defaultProvider":"deepseek","defaultModel":"bound-model"}`), 0600); err != nil {
		t.Fatal(err)
	}
	scope := uuid.NewString()
	c := records.Connection{ID: uuid.NewString(), Agent: "pi", Generation: 2, Enabled: true, AllowedScopes: []string{scope}}
	i, err := Install(context.Background(), c, InstallOptions{ClientRoot: root, ServiceRoot: filepath.Join(root, "service"), AgentPath: program, Bridge: program, PiDirectory: profile})
	if err != nil {
		t.Fatal(err)
	}
	if err = SaveProtocolVerification(root, c.ID, 2, "", ProtocolVerificationContext{ScopeDigest: protocolScopeDigest(c)}); err != nil {
		t.Fatal(err)
	}
	i, err = LoadInstallation(root, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if i.VerifiedGeneration != 0 || i.VerifiedAt != "" {
		t.Fatal("fixture forged legacy read/write proof")
	}
	workspace, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := ResolvePiProject(context.Background(), i, workspace, "")
	if err != nil {
		t.Fatalf("F9 current protocol-only connection rejected: %v", err)
	}
	if plan.Executor.ConnectionID != c.ID || plan.Executor.Generation != 2 || plan.Executor.Model != "bound-model" {
		t.Fatal(plan.Executor)
	}
	renewed := i
	renewed.ProtocolVerifiedAt = time.Now().Add(time.Minute).UTC().Format(time.RFC3339)
	renewedPlan, renewedErr := ResolvePiProject(context.Background(), renewed, workspace, "")
	if renewedErr != nil || renewedPlan.Executor.ConfigurationDigest == plan.Executor.ConfigurationDigest {
		t.Fatal("queued executor did not freeze its connection proof", renewedErr)
	}

	// Authorization/identity changes must not reuse an old protocol receipt.
	for _, change := range []func(*Installation){
		func(x *Installation) { x.Connection.Generation++ },
		func(x *Installation) { x.ProtocolScopeDigest = "outdated" },
		func(x *Installation) { x.Connection.AllowedScopes = []string{uuid.NewString()} },
		func(x *Installation) { x.Connection.Enabled = false },
		func(x *Installation) { x.Connection.AutoProject = true },
	} {
		bad := i
		change(&bad)
		if _, err = ResolvePiProject(context.Background(), bad, workspace, ""); err == nil {
			t.Fatal("changed authority accepted")
		}
	}
	if _, err = ResolvePiProject(context.Background(), i, filepath.Join(workspace, "missing"), ""); err == nil {
		t.Fatal("unavailable workspace accepted")
	}
	// Existing paid execution freeze remains in force before any process starts.
	if err = os.WriteFile(filepath.Join(profile, "settings.json"), []byte(`{"defaultProvider":"deepseek","defaultModel":"changed"}`), 0600); err != nil {
		t.Fatal(err)
	}
	task := records.ProjectTask{ID: uuid.NewString(), Attempt: 1, Snapshot: records.ProjectTaskSnapshot{Workspace: workspace, Executor: plan.Executor}}
	execution, err := RunPiProject(context.Background(), plan, task, nil, nil)
	if err == nil || execution.Started {
		t.Fatal("config drift launched execution", execution, err)
	}
}
