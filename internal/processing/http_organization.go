package processing

import (
	"context"

	"github.com/zwhu/memlane/internal/agents"
	"github.com/zwhu/memlane/internal/records"
)

type HTTPOrganizationExecution struct {
	HTTP        records.ProcessingHTTPReceipt `json:"http"`
	Result      *records.OrganizationResult   `json:"result,omitempty"`
	FailureCode string                        `json:"failure_code,omitempty"`
}

// RunHTTPOrganization owns no Agent process or tools. Its result is still only
// a candidate: exact source/version/approval rules are enforced by Decode and
// the existing leased service/store publication boundary.
func (o *Owner) RunHTTPOrganization(ctx context.Context, snapshot records.OrganizationSnapshot, onEvent func(string)) (out HTTPOrganizationExecution, resultErr error) {
	if snapshot.Processing == nil {
		return out, records.MergeError("configuration_required")
	}
	binding, err := o.ResolveTask(ctx, *snapshot.Processing)
	if err != nil {
		return out, err
	}
	if binding.Spec.Mode != "provider" && binding.Spec.Mode != "magpie" {
		return out, records.MergeError("unsupported_processing")
	}
	system, prompt, err := agents.OrganizationModelPrompt(snapshot)
	if err != nil {
		return out, err
	}
	result, err := o.completeHTTP(ctx, binding, system, prompt, snapshot.OutputSchema(), onEvent)
	out.HTTP = result.Receipt
	if err != nil {
		out.FailureCode = processingHTTPErrorCode(ctx, err)
		return out, err
	}
	if !result.Terminal || !result.Receipt.RequestSent || !result.Receipt.NativeSchemaRequested || !result.Receipt.ResponseComplete || !result.Receipt.SchemaValid {
		return out, records.MergeError("provider_response_invalid")
	}
	if _, err = o.ResolveTask(ctx, *snapshot.Processing); err != nil {
		out.FailureCode = "dependency_changed"
		return out, err
	}
	decoded, err := snapshot.Decode(result.Output)
	if err != nil {
		out.FailureCode = "invalid_proposal"
		return out, err
	}
	if ctx.Err() != nil {
		return out, ctx.Err()
	}
	out.Result = &decoded
	return out, nil
}
