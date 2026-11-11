package serviceipc

import (
	"context"

	"github.com/zwhu/memlane/internal/records"
)

func (l Library) ProcessingConfiguration(ctx context.Context) (records.ProcessingConfiguration, error) {
	var out records.ProcessingConfiguration
	err := Call(ctx, l.Root, Request{Op: "processing_get"}, &out)
	return out, err
}

func (l Library) SaveProcessing(ctx context.Context, draft records.ProcessingSave) (records.ProcessingRevision, error) {
	var out records.ProcessingRevision
	err := Call(ctx, l.Root, Request{Op: "processing_save", ProcessingSave: &draft, UserConfirmed: true}, &out)
	return out, err
}

func (l Library) ValidateProcessing(ctx context.Context, request records.ProcessingValidationRequest) (records.ProcessingValidation, error) {
	var out records.ProcessingValidation
	err := Call(ctx, l.Root, Request{Op: "processing_validate", ProcessingValidation: &request, UserConfirmed: true}, &out)
	return out, err
}

func (l Library) ProcessingValidation(ctx context.Context, id string) (records.ProcessingValidationStatus, error) {
	var out records.ProcessingValidationStatus
	err := Call(ctx, l.Root, Request{Op: "processing_validation", ProcessingValidationID: id}, &out)
	return out, err
}

func (l Library) CancelProcessingValidation(ctx context.Context, id string) error {
	return Call(ctx, l.Root, Request{Op: "processing_validation_cancel", ProcessingValidationID: id, UserConfirmed: true}, nil)
}

func (l Library) ActivateProcessing(ctx context.Context, request records.ProcessingActivation) (records.ProcessingActivationReceipt, error) {
	var out records.ProcessingActivationReceipt
	err := Call(ctx, l.Root, Request{Op: "processing_activate", ProcessingActivation: &request, UserConfirmed: true}, &out)
	return out, err
}

// SaveProcessingCredential consumes the supplied secret bytes. The private IPC
// response returns only immutable references and supplier metadata.
func (l Library) SaveProcessingCredential(ctx context.Context, d records.ProcessingCredentialWrite) (records.ProcessingCredentialReference, error) {
	defer clear(d.Secret)
	var out records.ProcessingCredentialReference
	err := Call(ctx, l.Root, Request{Op: "processing_credential_save", UserConfirmed: true, ProcessingCredentialWrite: &d}, &out)
	return out, err
}
func (l Library) ProcessingSuppliers(ctx context.Context) ([]records.ProcessingSupplier, error) {
	var out []records.ProcessingSupplier
	err := Call(ctx, l.Root, Request{Op: "processing_suppliers"}, &out)
	return out, err
}
func (l Library) ProcessingModels(ctx context.Context, q records.ProcessingModelQuery) (records.ProcessingModelCatalog, error) {
	var out records.ProcessingModelCatalog
	err := Call(ctx, l.Root, Request{Op: "processing_models", UserConfirmed: true, ProcessingModelQuery: &q}, &out)
	return out, err
}
