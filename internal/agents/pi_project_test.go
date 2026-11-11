//go:build darwin || linux

package agents

import (
	"context"
	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
	"os"
	"path/filepath"
	"testing"
)

func TestBoundPiProjectCapturesCurrentModelAndRejectsChangedConfiguration(t *testing.T) {
	root := t.TempDir()
	program := filepath.Join(root, "pi")
	if err := os.WriteFile(program, []byte("#!/bin/sh\nif test \"$1\" = --help; then echo --extension; else echo 0.84.1; fi\n"), 0700); err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(root, "pi-home")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "settings.json"), []byte(`{"defaultProvider":"deepseek","defaultModel":"bound-model"}`), 0600); err != nil {
		t.Fatal(err)
	}
	c := records.Connection{ID: uuid.NewString(), Agent: "pi", Generation: 1, Enabled: true, AllowedScopes: []string{uuid.NewString()}}
	installation, err := Install(context.Background(), c, InstallOptions{ClientRoot: root, ServiceRoot: filepath.Join(root, "service"), AgentPath: program, Bridge: program, PiDirectory: directory})
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ResolvePiProject(context.Background(), installation, workspace, ""); err == nil {
		t.Fatal("unverified installation selected")
	}
	if err = SaveVerification(root, c.ID, c.Generation, ""); err != nil {
		t.Fatal(err)
	}
	installation, err = LoadInstallation(root, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := ResolvePiProject(context.Background(), installation, workspace, "")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Executor.Model != "bound-model" || plan.Executor.ConnectionID != c.ID || plan.Executor.Agent != "pi" || len(plan.Executor.ConfigurationDigest) != 64 {
		t.Fatal("bound model replaced", plan.Executor)
	}
	if err = os.WriteFile(filepath.Join(directory, "models.json"), []byte(`{"providers":{"other":{}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	task := records.ProjectTask{ID: uuid.NewString(), Attempt: 1, Snapshot: records.ProjectTaskSnapshot{Workspace: workspace, Executor: plan.Executor}}
	execution, err := RunPiProject(context.Background(), plan, task, nil, nil)
	if err == nil || execution.Started {
		t.Fatal("configuration change launched a paid task", execution, err)
	}
	task.Snapshot.IncludeHistory = true
	if execution, err = RunPiProject(context.Background(), plan, task, nil, nil); err == nil || execution.Started {
		t.Fatal("unadapted history launched", err)
	}
}

func TestProjectProposalRejectsRepeatedSourceEvenWhenEveryQuoteIsExact(t *testing.T) {
	task := records.ProjectTask{ID: uuid.NewString(), Attempt: 1}
	source := records.ProjectRepositorySource{ID: hash([]byte("source")), Path: "queue.go", Text: "const MaxAttempts = 3\n\nfunc Deliver() {}\n"}
	proposal := records.ProjectKnowledgeProposal{Task: task.ID, Attempt: 1, Explanation: "synthetic", Items: []records.ProjectKnowledgeItem{{Title: "queue", Text: "at most three attempts", Sources: []records.ProjectKnowledgeQuote{{Source: source.ID, Quote: "const MaxAttempts = 3"}, {Source: source.ID, Quote: "func Deliver() {}"}}}}}
	if err := validateProjectProposal(proposal, task, []records.ProjectRepositorySource{source}); err == nil {
		t.Fatal("repeated source accepted despite citation identity contract")
	}
	proposal.Items[0].Sources = []records.ProjectKnowledgeQuote{{Source: source.ID, Quote: source.Text}}
	if err := validateProjectProposal(proposal, task, []records.ProjectRepositorySource{source}); err != nil {
		t.Fatal("one continuous exact source rejected", err)
	}
}
