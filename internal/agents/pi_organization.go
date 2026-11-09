package agents

import (
	"context"
	_ "embed"
	"encoding/json"
	"github.com/zwhu/memlane/internal/records"
)

//go:embed pi-structured-output.ts
var piStructuredOutput string

type PiOrganizationRunOptions struct {
	Executable, AuthHome, Provider, Model string
	GuardExecutable                       string
	OnStart                               func(int) error
	OnEvent                               func(string)
	Snapshot                              records.OrganizationSnapshot
}

type PiOrganizationExecution struct {
	AgentVersion     string                      `json:"agent_version,omitempty"`
	Result           *records.OrganizationResult `json:"result,omitempty"`
	SelectedProvider string                      `json:"selected_provider,omitempty"`
	SelectedModel    string                      `json:"selected_model,omitempty"`
	PiRPCProcess
}

// RunPiOrganization keeps the business snapshot contract over the generic model runner.
func RunPiOrganization(ctx context.Context, o PiOrganizationRunOptions) (out PiOrganizationExecution, resultErr error) {
	checked := o.Snapshot
	if checked.Seal() != nil || checked.Digest != o.Snapshot.Digest {
		return out, records.MergeError("invalid_proposal")
	}
	instructions := organizationPrompt
	if o.Snapshot.Schema == records.OrganizationReviewSchema {
		instructions = organizationReviewPrompt
	}
	if o.Snapshot.Schema == records.OrganizationGlobalSchema {
		instructions = organizationReviewPrompt + organizationGlobalPrompt
	}
	snapshot, _ := json.Marshal(o.Snapshot)
	prompt := instructions + "\n宿主指定result_id=" + o.Snapshot.ProposalID() + "\n严格输出schema：\n" + string(o.Snapshot.OutputSchema()) + "\n冻结授权材料：\n" + string(snapshot)
	execution, err := RunPiCompletion(ctx, PiCompletionOptions{Executable: o.Executable, AuthHome: o.AuthHome, Provider: o.Provider, Model: o.Model, SystemPrompt: instructions, Prompt: prompt, OutputSchema: o.Snapshot.OutputSchema(), GuardExecutable: o.GuardExecutable, OnStart: o.OnStart, OnEvent: o.OnEvent})
	out.AgentVersion, out.SelectedProvider, out.SelectedModel, out.PiRPCProcess = execution.AgentVersion, execution.SelectedProvider, execution.SelectedModel, execution.PiRPCProcess
	if err != nil {
		return out, err
	}
	decoded, err := o.Snapshot.Decode(execution.Final)
	if err != nil {
		out.Final = nil
		return out, err
	}
	out.Result = &decoded
	return out, nil
}
