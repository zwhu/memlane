package desktop

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/recoveryfiles"
	"github.com/zwhu/memlane/internal/store"
)

// RestoreBackup is an offline maintenance operation. The controller stops the
// service/worker first; the file manager then acquires both owner locks. The
// store helper opens only a private in-memory copy of the verified backup.
func (r *RuntimeController) RestoreBackup(ctx context.Context, backup, operation string) (records.RestoreReceipt, error) {
	var out records.RestoreReceipt
	r.Cancel()
	r.lifecycle.Lock()
	defer r.lifecycle.Unlock()
	if err := r.stopService(ctx); err != nil {
		return out, err
	}
	r.mu.Lock()
	if r.busy {
		r.mu.Unlock()
		return out, fmt.Errorf("component maintenance already running")
	}
	r.busy = true
	r.mu.Unlock()
	defer func() { r.mu.Lock(); r.busy = false; r.mu.Unlock() }()
	return (recoveryfiles.RestoreManager{Root: filepath.Join(r.Config.Root, "data")}).Restore(ctx, backup, operation, store.PrepareRestoredSnapshot)
}

func (a *App) ConfigureBackupRecovery(restore func(context.Context, string, string) (records.RestoreReceipt, error), reload func()) {
	a.restoreBackup, a.reloadAfterBackup = restore, reload
}

func (a *App) backupRestorePreview(c *ui.Context, r records.BackupReceipt) {
	b := &a.backups
	if b.RestoreSelection.ID != r.ID {
		quietButton(c, "从此备份恢复…").Key("backup-restore-open-" + r.ID).Disabled(b.Busy || b.RestoreLocked || r.Verification == "invalid" || a.restoreBackup == nil).OnClick(func() { a.previewBackupRestore(r.ID) })
		return
	}
	ui.Text(c, "恢复整个本机记忆库").Bold()
	ui.Text(c, fmt.Sprintf("恢复到 %s 的 schema %d 快照。备份之后的更改保存在恢复前副本中；原文、来源与历史按该快照恢复。", r.CreatedAt, r.Schema)).TextColor(c.Theme().TextMuted)
	ui.Text(c, "确认后停止服务与引擎，撤销旧 Agent 连接，停用全部整理计划和代码索引，待审核建议与未提交初始化失效。完成后 App 重新打开，服务保持停止；请明确启动并重新连接 Agent。").TextColor(c.Theme().TextMuted)
	ui.Row(c).Gap(8).Children(func() {
		quietButton(c, "确认恢复此备份").Key("backup-restore-confirm").Disabled(b.Busy).OnClick(a.confirmBackupRestore)
		quietButton(c, "取消恢复").Key("backup-restore-dismiss").Disabled(b.Busy || b.RestoreLocked).OnClick(func() { b.RestoreSelection = records.BackupReceipt{}; b.RestoreOperation = "" })
	})
}

func (a *App) previewBackupRestore(id string) {
	b := &a.backups
	if a.libraryContext == nil || b.Busy || b.RestoreLocked || a.restoreBackup == nil {
		return
	}
	b.Busy = true
	b.Sequence++
	b.Loading = false
	sequence, client, parent := b.Sequence, b.Client, a.libraryContext
	b.Error = ""
	go func() {
		ctx, cancel := context.WithTimeout(parent, 30*time.Second)
		defer cancel()
		r, err := client.Verify(ctx, id)
		if parent.Err() != nil {
			return
		}
		a.dispatch(func() {
			if sequence != b.Sequence {
				return
			}
			b.Busy = false
			if err != nil {
				b.Error = "备份文件校验失败，无法预览恢复；当前库保留。"
				return
			}
			b.RestoreSelection = r
			b.RestoreOperation = ""
			b.Notice = ""
		})
	}()
}

func (a *App) confirmBackupRestore() {
	b := &a.backups
	if a.libraryContext == nil || b.Busy || b.RestoreSelection.ID == "" || a.restoreBackup == nil {
		return
	}
	if b.RestoreOperation == "" {
		b.RestoreOperation = uuid.NewString()
	}
	b.Busy, b.RestoreLocked = true, true
	b.Sequence++
	b.Loading = false
	sequence, parent := b.Sequence, a.libraryContext
	backup, operation, restore := b.RestoreSelection.ID, b.RestoreOperation, a.restoreBackup
	b.Error = ""
	b.Notice = "正在停止服务并恢复；完成后 App 重新打开，服务保持停止…"
	go func() {
		ctx, cancel := context.WithTimeout(parent, 2*time.Minute)
		defer cancel()
		r, err := restore(ctx, backup, operation)
		if parent.Err() != nil {
			return
		}
		a.dispatch(func() {
			if sequence != b.Sequence {
				return
			}
			b.Busy = false
			if err != nil {
				b.Notice = ""
				b.Error = "恢复未完成，请核对停服状态；已有文件保留，再次确认沿用同一恢复 ID。原因：" + err.Error()
				return
			}
			b.LastRestore = r
			b.Notice = "恢复完成，服务保持停止 · " + r.ID
			a.runtimeStatus = RuntimeStatus{Phase: "stopped", Message: "备份恢复完成，请明确启动服务并重新连接 Agent", Installed: true}
			if a.reloadAfterBackup != nil {
				a.reloadAfterBackup()
			}
		})
	}()
}
