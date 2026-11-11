package daemon

import (
	"context"
	"github.com/zwhu/memlane/internal/agents"
	"github.com/zwhu/memlane/internal/records"
)

type organizationModelExecution struct {
	agents.ProposalProcess
	Result *records.OrganizationResult
	Detail any
}

func (s *Server) runOrganizationModel(ctx context.Context, t records.OrganizationTask, model string, onStart func(int) error, onEvent func(string)) (organizationModelExecution, error) {
	if t.Snapshot.Processing == nil {
		out, err := agents.RunOrganization(ctx, agents.OrganizationRunOptions{Snapshot: t.Snapshot, Model: model, GuardExecutable: s.proposalGuard, OnStart: onStart, OnEvent: onEvent})
		return organizationModelExecution{ProposalProcess: out.ProposalProcess, Result: out.Result, Detail: out.ProposalProcess}, err
	}
	if s.processing == nil {
		return organizationModelExecution{}, records.MergeError("unsupported_processing")
	}
	binding, err := s.processing.ResolveTask(ctx, *t.Snapshot.Processing)
	if err != nil {
		return organizationModelExecution{}, err
	}
	if binding.Spec.Mode == "provider" || binding.Spec.Mode == "magpie" {
		out, err := s.processing.RunHTTPOrganization(ctx, t.Snapshot, onEvent)
		return organizationModelExecution{Result: out.Result, Detail: out}, err
	}
	if binding.Spec.Agent == "codex" {
		out, err := s.processing.RunCodexOrganization(ctx, t.Snapshot, onStart, onEvent)
		return organizationModelExecution{ProposalProcess: out.ProposalProcess, Result: out.Result, Detail: out}, err
	}
	out, err := agents.RunPiOrganization(ctx, agents.PiOrganizationRunOptions{Snapshot: t.Snapshot, Executable: binding.Executable, AuthHome: binding.AuthHome, Provider: binding.Spec.Provider, Model: binding.Spec.Model, GuardExecutable: binding.Helper, OnStart: onStart, OnEvent: onEvent})
	result := organizationModelExecution{ProposalProcess: out.ProposalProcess, Result: out.Result, Detail: out}
	if err == nil {
		_, err = s.processing.ResolveTask(ctx, *t.Snapshot.Processing)
		if err != nil {
			result.Result = nil
		}
	}
	return result, err
}
