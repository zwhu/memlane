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
func (c BackupClient) Restores(ctx context.Context) ([]records.RestoreReceipt, error) {
	return (recoveryfiles.RestoreManager{Root: c.Root}).List(ctx)
}

func (c BackupClient) Cancel(ctx context.Context, id string) (records.BackupOperation, error) {
	var out records.BackupOperation
	err := Call(ctx, c.Root, Request{Op: "backup_cancel", BackupID: id, UserConfirmed: true}, &out)
	return out, err
}
func (c BackupClient) Status(ctx context.Context, id string) (records.BackupOperation, error) {
	var out records.BackupOperation
	err := Call(ctx, c.Root, Request{Op: "backup_status", BackupID: id}, &out)
	return out, err
}
