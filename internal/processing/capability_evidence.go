package processing

import (
	"context"
	"github.com/zwhu/memlane/internal/agents"
	"github.com/zwhu/memlane/internal/records"
	"time"
)

type agentEvidenceLedger interface {
	FinishAgentProcessingValidation(context.Context, string, int64, string, string, string, time.Time, records.ProcessingValidationDiagnostics, *records.ProcessingCapabilityEvidence) error
}

func observedCapability(v records.ProcessingValidation, b Binding, x agents.PiCompletionExecution) *records.ProcessingCapabilityEvidence {
	if x.Observation == nil {
		return nil
	}
	o := x.Observation
	return &records.ProcessingCapabilityEvidence{ValidationID: v.ID, Revision: v.Revision, Attempt: v.Attempt, DependencyDigest: v.DependencyDigest, Agent: b.Spec.Agent, ResponseComplete: o.ResponseComplete, StructureValid: o.LocalSchemaValid, IsolationConfigured: o.IsolationConfigured, IsolationPolicy: o.IsolationPolicy, Reaped: x.Reaped, GroupGone: x.GroupGone}
}
