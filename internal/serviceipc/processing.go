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
