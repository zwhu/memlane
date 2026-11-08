package serviceipc

import (
	"context"

	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/recoveryfiles"
)

type BackupClient struct{ Root string }

func (c BackupClient) Create(ctx context.Context, id string) (records.BackupReceipt, error) {
	var out records.BackupReceipt
	err := Call(ctx, c.Root, Request{Op: "backup_create", BackupID: id, UserConfirmed: true}, &out)
	return out, err
}
func (c BackupClient) List(ctx context.Context) ([]records.BackupReceipt, error) {
	out, err := (recoveryfiles.Manager{Root: c.Root}).List(ctx)
	return out, err
}
func (c BackupClient) Verify(ctx context.Context, id string) (records.BackupReceipt, error) {
	snapshot, err := (recoveryfiles.Manager{Root: c.Root}).Verify(ctx, id)
	return snapshot.Receipt, err
}
