package processing

import (
	"context"
	"github.com/zwhu/memlane/internal/records"
)

func (o *Owner) ResolveCurrentTask(ctx context.Context, request records.ProcessingTaskAuthorization) (PiBinding, error) {
	configuration, err := o.Ledger.ProcessingConfiguration(ctx)
	if err != nil {
		return PiBinding{}, err
	}
	current := configuration.Current
	if !current.Active || current.Revision == nil {
		return PiBinding{}, records.MergeError("configuration_required")
	}
	if current.Generation != request.Generation || current.Revision.ID != request.Revision {
		return PiBinding{}, records.ErrConflict
	}
	return o.resolve(ctx, current.Revision.Spec)
}

// Running tasks retain their frozen selection even when the settings page
// switches. Changed runtime dependencies or revoked connections do invalidate it.
func (o *Owner) ResolveTask(ctx context.Context, snapshot records.ProcessingSnapshot) (PiBinding, error) {
	v, err := o.Ledger.ProcessingRevision(ctx, snapshot.Revision.ID)
	if err != nil {
		return PiBinding{}, err
	}
	if v.Digest != snapshot.Revision.Digest || v.Validation == nil || v.Validation.State != "succeeded" || v.Validation.CancelRequested || v.Validation.DependencyDigest != snapshot.DependencyDigest {
		return PiBinding{}, records.MergeError("verification_required")
	}
	b, err := o.resolve(ctx, v.Spec)
	if err != nil {
		return PiBinding{}, err
	}
	if b.Dependency != snapshot.DependencyDigest {
		return PiBinding{}, records.MergeError("dependency_changed")
	}
	return b, nil
}
