//go:build darwin || linux

package agents

import (
	"context"
	"github.com/zwhu/memlane/internal/records"
	"path/filepath"
)

type P3RunOptions struct {
	Agent      string // codex (default) or traex; experimental adapters only
	Model      string // preserve the selected traex model without loading user config
	Executable string
	AuthHome   string
	Snapshot   records.P3Snapshot
	// Called with event type only; no reasoning or private model trace is saved.
	OnEvent func(string)
}

type P3Execution struct {
	Proposal *records.P3Proposal `json:"proposal,omitempty"`
	ProposalProcess
}

func RunP3Experiment(ctx context.Context, o P3RunOptions) (P3Execution, error) {
	check := o.Snapshot
	if check.Seal() != nil || check.Digest != o.Snapshot.Digest || !filepath.IsAbs(o.Executable) {
		return P3Execution{}, records.ErrP3Invalid
	}
	process, err := runIsolatedProposal(ctx, isolatedProposalOptions{Agent: o.Agent, Model: o.Model, Executable: o.Executable, AuthHome: o.AuthHome, OnEvent: o.OnEvent}, o.Snapshot, p3ProposalPrompt)
	out := P3Execution{ProposalProcess: process}
	if err != nil {
		return out, err
	}
	p, err := o.Snapshot.Decode(process.Final)
	if err != nil {
		return out, err
	}
	out.Proposal = &p
	return out, nil
}

func runP3Process(ctx context.Context, executable string, args, env []string, dir, prompt string, x records.P3Snapshot, onEvent func(string)) (P3Execution, error) {
	process, err := runProposalProcess(ctx, executable, args, env, dir, prompt, onEvent)
	out := P3Execution{ProposalProcess: process}
	if err != nil {
		return out, err
	}
	p, err := x.Decode(process.Final)
	if err != nil {
		return out, err
	}
	out.Proposal = &p
	return out, nil
}
