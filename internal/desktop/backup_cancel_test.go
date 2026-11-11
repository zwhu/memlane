package desktop

import (
	"testing"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
)

func TestBackupCancellationUsesTerminalPublicationReceipt(t *testing.T) {
	for _, state := range []string{"cancelled", "completed"} {
		t.Run(state, func(t *testing.T) {
			id := uuid.NewString()
			a, dispatched := processingUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
				if r.Op != "backup_cancel" || r.BackupID != id {
					return serviceipc.Response{Error: "wrong backup operation"}
				}
				result := records.BackupOperation{ID: id, State: state}
				if state == "completed" {
					result.Receipt = &records.BackupReceipt{ID: id}
				}
				return serviceipc.Response{OK: true, Result: result}
			})
			a.backups = BackupUI{Client: serviceipc.BackupClient{Root: a.library.Root}, Operation: id, Busy: true, Creating: true, Sequence: 5}
			a.cancelBackup()
			if !a.backups.Busy || !a.backups.Cancelling || a.backups.VerifiedID != "" {
				t.Fatal("cancel request fabricated terminal outcome")
			}
			awaitProcessingDispatch(t, dispatched)
			if a.backups.Busy || a.backups.Cancelling || a.backups.Creating || a.backups.Operation != "" || a.backups.Sequence != 6 {
				t.Fatal("terminal cancellation did not retire delayed create response", a.backups)
			}
			if state == "cancelled" && a.backups.Notice != "备份已取消，未发布成功快照。" {
				t.Fatal("cancel outcome missing")
			}
			if state == "completed" && a.backups.VerifiedID != id {
				t.Fatal("published backup was hidden by late cancel")
			}
		})
	}
}
