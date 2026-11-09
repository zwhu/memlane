package daemon_test

// Actual private IPC and SQLite persistence; this deliberately never starts
// Provider inference or claims a validation receipt from an API fixture.
import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/daemon"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
)

func TestProcessingPrivateIPCOwnershipAndSaveWithoutActivation(t *testing.T) {
	root := t.TempDir()
	stop := startService(t, daemon.Config{Root: root})
	defer stop()
	ctx := context.Background()
	library := serviceipc.Library{Root: root}
	before, err := library.ProcessingConfiguration(ctx)
	requireService(t, err)
	if len(before.Saved) != 0 || before.Current.Active || before.Current.Revision != nil {
		t.Fatal("new daemon enabled a model")
	}
	draft := records.ProcessingSave{OperationID: uuid.NewString(), Spec: records.ProcessingSpec{Mode: "provider", Provider: "IPC fixture", Model: "unverified-fixture", Protocol: "openai-responses", Endpoint: "https://provider.invalid/v1"}}
	if err = serviceipc.Call(ctx, root, serviceipc.Request{Op: "processing_save", ProcessingSave: &draft}, nil); err == nil {
		t.Fatal("configuration save bypassed user action gate")
	}
	revision, err := library.SaveProcessing(ctx, draft)
	requireService(t, err)
	replayed, err := library.SaveProcessing(ctx, draft)
	requireService(t, err)
	if replayed.ID != revision.ID {
		t.Fatal("IPC retry duplicated a revision")
	}
	after, err := library.ProcessingConfiguration(ctx)
	requireService(t, err)
	if after.Current.Active || after.Current.Revision != nil || after.Saved["provider"].Validation != nil {
		t.Fatal("saving config fabricated activation or validation", after)
	}
	if err = serviceipc.Call(ctx, root, serviceipc.Request{Op: "processing_validate", ProcessingValidation: &records.ProcessingValidationRequest{OperationID: uuid.NewString(), Revision: revision.ID}}, nil); err == nil {
		t.Fatal("private IPC validation bypassed user confirmation")
	}
	if _, err = library.ValidateProcessing(ctx, records.ProcessingValidationRequest{OperationID: uuid.NewString(), Revision: revision.ID}); err == nil {
		t.Fatal("unsupported Provider validation was accepted without a real runner")
	}
	// No RPC is provided for a UI/Agent to assert successful capability.
	if err = serviceipc.Call(ctx, root, serviceipc.Request{Op: "processing_finish", UserConfirmed: true}, nil); err == nil {
		t.Fatal("private API accepted fabricated processing capability")
	}
	for _, kind := range []string{"raw", "knowledge"} {
		var page records.Page
		requireService(t, serviceipc.Call(ctx, root, serviceipc.Request{Op: "list", ListQuery: records.ListQuery{Kind: kind}}, &page))
		if page.Total != 0 {
			t.Fatal("configuration created memory material", kind, page.Total)
		}
	}
}
