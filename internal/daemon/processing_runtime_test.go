package daemon

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/agents"
	"github.com/zwhu/memlane/internal/processing"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/store"
)

func processingRuntimeServer(t *testing.T, run func(context.Context, processing.PiBinding, func(int) error, func(string)) (agents.PiCompletionExecution, error)) (*Server, *store.Store, records.ProcessingRevision) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "memlane.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	scopes, err := db.Scopes(context.Background())
	if err != nil || len(scopes) == 0 {
		t.Fatal(err)
	}
	connection, err := db.ConfigureConnection(context.Background(), records.ConnectionDraft{Agent: "pi", Name: "daemon processing fixture", AllowedScopes: []string{scopes[0].ID}})
	if err != nil {
		t.Fatal(err)
	}
	revision, err := db.SaveProcessing(context.Background(), records.ProcessingSave{OperationID: uuid.NewString(), Spec: records.ProcessingSpec{Mode: "agent", Agent: "pi", Provider: "deepseek", Model: "deepseek-flash", Connection: connection.ID, ConnectionGeneration: connection.Generation}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	lifecycle, stop := context.WithCancel(context.Background())
	s := &Server{lifecycle: lifecycle, stop: stop, store: db, gate: make(chan struct{}, 1)}
	s.processing = &processing.Owner{Ledger: db, Lifecycle: lifecycle, Acquire: s.acquireOrganizer,
		Resolve: func(_ context.Context, _, _, _ string, spec records.ProcessingSpec) (processing.PiBinding, error) {
			return processing.PiBinding{Spec: spec, Dependency: records.MergeDigest("daemon processing fixture")}, nil
		}, Run: run, Poison: stop}
	t.Cleanup(func() { stop(); s.processing.Close() })
	return s, db, revision
}

func processingExecution(binding processing.PiBinding) agents.PiCompletionExecution {
	return agents.PiCompletionExecution{SelectedProvider: binding.Spec.Provider, SelectedModel: binding.Spec.Model,
		PiRPCProcess: agents.PiRPCProcess{NativeSchemaRequested: true,
			ProposalProcess: agents.ProposalProcess{Started: true, Terminal: true, Reaped: true, GroupGone: true}}}
}

func waitProcessingStatus(t *testing.T, s *Server, id string) records.ProcessingValidationStatus {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		value, err := s.perform(context.Background(), Request{Op: "processing_validation", ProcessingValidationID: id})
		if err != nil {
			t.Fatal(err)
		}
		status := value.(records.ProcessingValidationStatus)
		if status.Validation.State != "validating" {
			return status
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("processing validation did not finish")
	return records.ProcessingValidationStatus{}
}

func TestProcessingRuntimePrivateActionsValidateThenActivate(t *testing.T) {
	s, db, revision := processingRuntimeServer(t, func(_ context.Context, binding processing.PiBinding, onStart func(int) error, onEvent func(string)) (agents.PiCompletionExecution, error) {
		if err := onStart(4242); err != nil {
			return agents.PiCompletionExecution{}, err
		}
		onEvent("message_start")
		return processingExecution(binding), nil
	})
	request := records.ProcessingValidationRequest{OperationID: uuid.NewString(), Revision: revision.ID}
	if _, err := s.perform(context.Background(), Request{Op: "processing_validate", ProcessingValidation: &request}); err == nil {
		t.Fatal("validation bypassed explicit user action")
	}
	value, err := s.perform(context.Background(), Request{Op: "processing_validate", ProcessingValidation: &request, UserConfirmed: true})
	if err != nil {
		t.Fatal(err)
	}
	validation := value.(records.ProcessingValidation)
	status := waitProcessingStatus(t, s, validation.ID)
	if status.Validation.State != "succeeded" || status.Validation.ReceiptDigest == "" {
		t.Fatal("daemon did not persist owned validation", status)
	}
	activation := records.ProcessingActivation{OperationID: uuid.NewString(), Revision: revision.ID, ExpectedGeneration: 0}
	if _, err = s.perform(context.Background(), Request{Op: "processing_activate", ProcessingActivation: &activation}); err == nil {
		t.Fatal("activation bypassed explicit user action")
	}
	value, err = s.perform(context.Background(), Request{Op: "processing_activate", ProcessingActivation: &activation, UserConfirmed: true})
	if err != nil || value.(records.ProcessingActivationReceipt).Generation != 1 {
		t.Fatal("daemon did not activate verified configuration", value, err)
	}
	configuration, err := db.ProcessingConfiguration(context.Background())
	if err != nil || !configuration.Current.Active || configuration.Current.Revision == nil || configuration.Current.Revision.ID != revision.ID {
		t.Fatal("current processing selection was not persisted", configuration, err)
	}
}

func TestProcessingRuntimeCancelStopsOwnedValidation(t *testing.T) {
	started := make(chan struct{})
	s, _, revision := processingRuntimeServer(t, func(ctx context.Context, _ processing.PiBinding, onStart func(int) error, _ func(string)) (agents.PiCompletionExecution, error) {
		if err := onStart(4243); err != nil {
			return agents.PiCompletionExecution{}, err
		}
		close(started)
		<-ctx.Done()
		return agents.PiCompletionExecution{}, ctx.Err()
	})
	value, err := s.perform(context.Background(), Request{Op: "processing_validate", ProcessingValidation: &records.ProcessingValidationRequest{OperationID: uuid.NewString(), Revision: revision.ID}, UserConfirmed: true})
	if err != nil {
		t.Fatal(err)
	}
	validation := value.(records.ProcessingValidation)
	<-started
	if _, err = s.perform(context.Background(), Request{Op: "processing_validation_cancel", ProcessingValidationID: validation.ID}); err == nil {
		t.Fatal("cancellation bypassed explicit user action")
	}
	if _, err = s.perform(context.Background(), Request{Op: "processing_validation_cancel", ProcessingValidationID: validation.ID, UserConfirmed: true}); err != nil {
		t.Fatal(err)
	}
	status := waitProcessingStatus(t, s, validation.ID)
	if status.Validation.State != "cancelled" || status.Validation.ReceiptDigest != "" {
		t.Fatal("cancelled validation retained success", status)
	}
}
