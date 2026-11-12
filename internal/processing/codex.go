package processing

import (
	"context"
	"encoding/json"
	"path/filepath"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/agents"
	"github.com/zwhu/memlane/internal/records"
)

func ResolveCodex(ctx context.Context, clientRoot, serviceRoot, helper string, spec records.ProcessingSpec) (Binding, error) {
	var out Binding
	id, err := uuid.Parse(spec.Connection)
	if err != nil || id.String() != spec.Connection || spec.Mode != "agent" || spec.Agent != "codex" || spec.Provider == "" || spec.Model == "" || spec.ConnectionGeneration < 1 {
		return out, records.MergeError("unsupported_processing")
	}
	if !filepath.IsAbs(clientRoot) || !filepath.IsAbs(serviceRoot) || !filepath.IsAbs(helper) {
		return out, records.MergeError("invalid_dependency")
	}
	i, err := agents.LoadInstallation(clientRoot, spec.Connection)
	verification, verified := agents.ConnectionVerification(i, spec.ConnectionGeneration)
	if err != nil || !verified || !i.Connection.Enabled || i.Connection.Agent != "codex" || i.Connection.ID != spec.Connection || i.Connection.Generation != spec.ConnectionGeneration || i.ToolsetVersion != 2 || i.Options.ClientRoot != clientRoot || i.Options.ServiceRoot != serviceRoot || !filepath.IsAbs(i.Options.CodexDirectory) || !filepath.IsAbs(i.Options.AgentPath) {
		return out, records.MergeError("connection_verification_required")
	}
	if agents.CheckInstallation(i) != nil {
		return out, records.MergeError("connection_changed")
	}
	plan, err := agents.ResolveCodexCompletion(ctx, agents.CodexCompletionOptions{Executable: i.Options.AgentPath, AuthHome: i.Options.CodexDirectory, Provider: spec.Provider, Model: spec.Model, GuardExecutable: helper})
	if err != nil {
		return out, records.MergeError(agents.CodexCompletionErrorCode(err))
	}
	parts := map[string]string{"adapter_protocol": records.MergeDigest(agents.CodexCompletionProtocol), "processing_configuration": records.MergeDigest(spec), "agent_version": records.MergeDigest(plan.Version), "agent_executable": records.MergeDigest(i.Options.AgentPath), "agent_configuration_directory": records.MergeDigest(i.Options.CodexDirectory), "helper_executable": records.MergeDigest(helper), "extension": records.MergeDigest(i.Block), "connection_verification": records.MergeDigest(verification), "independent_credential": plan.ConfigurationDigest}
	for _, name := range []string{"config.toml", "auth.json"} {
		digest, e := fileDigest(filepath.Join(i.Options.CodexDirectory, name), name == "auth.json")
		if e != nil {
			return out, e
		}
		parts[name] = digest
	}
	return Binding{Spec: spec, Executable: i.Options.AgentPath, AuthHome: i.Options.CodexDirectory, Helper: helper, AgentVersion: plan.Version, DependencyParts: parts, Dependency: records.MergeDigest(parts), Codex: &plan}, nil
}

func VerifyCodex(ctx context.Context, b Binding, onStart func(int) error, onEvent func(string)) (agents.PiCompletionExecution, error) {
	schema := json.RawMessage(`{"type":"object","additionalProperties":false,"required":["marker","value"],"properties":{"marker":{"type":"string","const":"memlane-model-probe"},"value":{"type":"integer","const":7}}}`)
	return CompleteCodex(ctx, b, "You are performing a synthetic model capability probe. Return the requested JSON only.", "Return marker memlane-model-probe and integer value 7. This probe contains no user knowledge.", schema, onStart, onEvent)
}

func CompleteCodex(ctx context.Context, b Binding, system, prompt string, schema json.RawMessage, onStart func(int) error, onEvent func(string)) (agents.PiCompletionExecution, error) {
	if b.Spec.Mode != "agent" || b.Spec.Agent != "codex" || b.Codex == nil {
		return agents.PiCompletionExecution{}, records.MergeError("unsupported_processing")
	}
	return agents.RunCodexCompletion(ctx, agents.CodexCompletionOptions{Binding: b.Codex, Executable: b.Executable, AuthHome: b.AuthHome, Provider: b.Spec.Provider, Model: b.Spec.Model, GuardExecutable: b.Helper, SystemPrompt: system, Prompt: prompt, OutputSchema: schema, OnStart: onStart, OnEvent: onEvent})
}

// The app-server Schema ACK is separate from both local validation and process
// cleanup. No HTTP response/status is invented for this Agent transport.
func CodexCompletionConfirmed(result agents.PiCompletionExecution, binding Binding) bool {
	proof := result.Codex
	return proof != nil && proof.Thread != "" && proof.Turn != "" && proof.PermissionProfile == "memlane_completion" && proof.SchemaDigest != "" && proof.SourceSchemaDigest != "" && proof.OutputSchemaAccepted && proof.FinalSchemaValid && proof.ToolFree && result.Accepted && result.Terminal && result.Reaped && result.GroupGone && result.NativeSchemaRequested && result.SelectedProvider == binding.Spec.Provider && result.SelectedModel == binding.Spec.Model
}

type CodexOrganizationExecution struct {
	agents.PiCompletionExecution
	Result *records.OrganizationResult `json:"result,omitempty"`
}

func (o *Owner) RunCodexOrganization(ctx context.Context, snapshot records.OrganizationSnapshot, onStart func(int) error, onEvent func(string)) (out CodexOrganizationExecution, resultErr error) {
	if snapshot.Processing == nil {
		return out, records.MergeError("configuration_required")
	}
	binding, err := o.ResolveTask(ctx, *snapshot.Processing)
	if err != nil {
		return out, err
	}
	system, prompt, err := agents.OrganizationModelPrompt(snapshot)
	if err != nil {
		return out, err
	}
	out.PiCompletionExecution, err = CompleteCodex(ctx, binding, system, prompt, snapshot.OutputSchema(), onStart, onEvent)
	if err != nil {
		return out, err
	}
	if !CodexCompletionConfirmed(out.PiCompletionExecution, binding) {
		out.Final = nil
		return out, records.MergeError("completion_unconfirmed")
	}
	if _, err = o.ResolveTask(ctx, *snapshot.Processing); err != nil {
		out.Final = nil
		return out, err
	}
	decoded, err := snapshot.Decode(out.Final)
	if err != nil {
		out.Final = nil
		out.FailureStage = "proposal_validation"
		out.FailureCode = "codex_proposal_invalid"
		return out, err
	}
	if err = ctx.Err(); err != nil {
		out.Final = nil
		return out, err
	}
	out.Result = &decoded
	return out, nil
}
