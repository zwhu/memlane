package desktop

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
)

type BackupUI struct {
	TechnicalDetails                                                 map[string]bool
	SelectedID                                                       string
	RestoreSelection                                                 records.BackupReceipt
	LastRestore                                                      records.RestoreReceipt
	RestoreOperation                                                 string
	RestoreLocked                                                    bool
	RestoreCatalogueError                                            string
	Client                                                           serviceipc.BackupClient
	Items                                                            []records.BackupReceipt
	Busy, Loading, Loaded                                            bool
	Creating, Cancelling                                             bool
	Offset                                                           int
	Operation, Notice, Error, CatalogueError, VerifiedID, VerifiedAt string
	Sequence                                                         int64
}

func (a *App) backupPage(c *ui.Context) {
	b := &a.backups
	ui.Row(c).Shrink(0).MinHeight(54).Padding(14, 28).Gap(10).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(design.Border).Children(func() {
		ui.Text(c, "备份与恢复").FontSize(design.Small).TextColor(design.Muted)
		ui.Spacer(c)
		if b.RestoreSelection.ID == "" {
			quietButton(c, "创建一致性备份").Key("backup-create").Disabled(a.libraryContext == nil || b.Busy || b.RestoreLocked).OnClick(a.createBackup)
		}
	})
	ui.Scroll(c.Key("maintenance-backup-body")).Grow(1).MinHeight(0).Padding(28).Gap(20).Children(func() {
		a.maintenanceStorageNotice(c, "backup", b.Operation)
		a.maintenanceStorageNotice(c, "restore", b.RestoreOperation)
		if b.RestoreSelection.ID != "" {
			a.backupRestoreDescription(c, b.RestoreSelection)
			if b.Notice != "" {
				ui.Text(c, b.Notice).Selectable()
			}
			if b.Error != "" {
				ui.Text(c, b.Error).Selectable()
			}
			return
		}
		a.backupBody(c, false)
	})
	ui.Row(c).Key("maintenance-backup-footer").MinHeight(64).Shrink(0).Padding(14, 24).Gap(10).AlignItems(ui.Center).BorderWidth(1, 0, 0, 0).BorderColor(design.Border).Children(func() {
		ui.Text(c, "备份不包含模型缓存与访问凭据").FontSize(10).TextColor(design.Muted).Grow(1).MinWidth(0)
		if b.RestoreSelection.ID != "" {
			a.backupRestoreActions(c)
		} else if b.Creating {
			actionButton(c, "取消本次备份", nil).Key("backup-cancel").Disabled(b.Cancelling).OnClick(a.cancelBackup)
		} else {
			actionButton(c, "验证所选备份", nil).Key("backup-verify-selected").Disabled(b.Busy || b.SelectedID == "").OnClick(func() { a.verifyBackup(b.SelectedID) })
		}
	})
}

func (a *App) backupSettings(c *ui.Context) {
	a.backupBody(c, true)
}

func (a *App) backupBody(c *ui.Context, inline bool) {
	b := &a.backups
	ui.Column(c).Gap(8).Children(func() {
		ui.Text(c, "本机一致性快照").FontSize(design.Small).TextColor(design.Muted)
		ui.Text(c, "给知识留下可恢复的副本").FontSize(design.Heading).Bold()
		ui.Text(c, "快照包含知识、原始记录、来源、版本和相关业务记录。引擎、模型和代码仓库单独管理；在线备份上限 64 MiB。").TextColor(design.Muted)
	})
	if inline {
		quietButton(c, "创建一致性备份").Key("backup-create").Disabled(a.libraryContext == nil || b.Busy || b.RestoreLocked).OnClick(a.createBackup)
	}
	if inline && b.Creating {
		quietButton(c, "取消本次备份").Key("backup-cancel").Disabled(b.Cancelling).OnClick(a.cancelBackup)
	}
	if b.Notice != "" {
		ui.Text(c, b.Notice).Selectable()
	}
	if b.Error != "" {
		ui.Text(c, b.Error).Selectable()
	}
	if b.CatalogueError != "" {
		ui.Text(c, b.CatalogueError).Selectable()
	}
	if b.RestoreCatalogueError != "" {
		ui.Text(c, b.RestoreCatalogueError).Selectable()
	}
	if r := b.LastRestore; r.ID != "" {
		ui.Text(c, "最近完整恢复 · "+backupDisplayTime(r.CreatedAt)).Bold()
		a.maintenanceTechnicalDetails(c, "restore:"+r.ID, func() {
			ui.Text(c, "恢复记录 · "+r.ID).FontSize(11).Selectable()
			ui.Text(c, "恢复时间 · "+r.CreatedAt).FontSize(11).Selectable()
			ui.Text(c, "源备份 · "+r.Backup).FontSize(11).Selectable()
		})
		ui.Text(c, fmt.Sprintf("当次恢复：%d 条原文等待重建 · %d 个旧连接退休 · %d 个计划停用 · %d 个代码索引停用", r.Stats.Memories, r.Stats.Connections, r.Stats.Plans, r.Stats.CodeWorktrees)).FontSize(11)
	}
	if len(b.Items) == 0 && b.Loaded && !b.Loading && b.CatalogueError == "" {
		ui.Text(c, "尚无手动备份。").TextColor(c.Theme().TextMuted)
	}
	if !b.Loaded && b.CatalogueError == "" {
		ui.Text(c, "尚未读取备份记录。").TextColor(c.Theme().TextMuted)
	}
	for _, r := range b.Items[min(b.Offset, len(b.Items)):min(b.Offset+10, len(b.Items))] {
		if b.SelectedID == "" {
			b.SelectedID = r.ID
		}
		ui.Column(c).Key("backup-record-"+r.ID).Padding(16, 0).Gap(8).BorderWidth(1, 0, 0, 0).BorderColor(design.Border).Children(func() {
			if !inline {
				quietButton(c, "选择备份 · "+backupDisplayTime(r.CreatedAt)).Key("backup-select-" + r.ID).AlignSelf(ui.Start).Bold().OnClick(func() { b.SelectedID = r.ID })
				if b.SelectedID == r.ID {
					ui.Text(c, "当前选择").FontSize(10).TextColor(design.Muted)
				}
			}
			if r.Verification == "invalid" {
				ui.Text(c, "备份记录损坏或不可读，不能用于恢复。").FontSize(11)
			} else {
				ui.Text(c, fmt.Sprintf("%s · %.2f MB", backupDisplayTime(r.CreatedAt), float64(r.Bytes)/1e6)).FontSize(11).Selectable()
			}
			a.maintenanceTechnicalDetails(c, "backup:"+r.ID, func() {
				ui.Text(c, "备份 · "+r.ID).FontSize(11).Selectable()
				ui.Text(c, "创建时间 · "+r.CreatedAt).FontSize(11).Selectable()
				if b.VerifiedID == r.ID && b.VerifiedAt != "" {
					ui.Text(c, "校验时间 · "+b.VerifiedAt).FontSize(11).Selectable()
				}
				ui.Text(c, fmt.Sprintf("快照格式 · schema %d", r.Schema)).FontSize(11).Selectable()
			})
			if inline {
				quietButton(c, "校验此备份").Key("backup-verify-" + r.ID).Disabled(b.Busy).OnClick(func() { a.verifyBackup(r.ID) })
			}
			if b.VerifiedID == r.ID {
				ui.Text(c, "上次校验通过 · "+backupDisplayTime(b.VerifiedAt)).FontSize(11).TextColor(c.Theme().TextMuted)
			} else {
				ui.Text(c, "当前文件待校验").FontSize(11).TextColor(c.Theme().TextMuted)
			}
			a.backupRestorePreview(c, r)
		})
	}
	if len(b.Items) > 10 {
		ui.Row(c).Gap(8).Children(func() {
			quietButton(c, "较新备份").Disabled(b.Offset == 0).OnClick(func() { b.Offset = max(0, b.Offset-10) })
			quietButton(c, "更早备份").Disabled(b.Offset+10 >= len(b.Items)).OnClick(func() { b.Offset += 10 })
		})
	}
}
func (a *App) refreshBackups() {
	b := &a.backups
	if a.libraryContext == nil || b.Loading || b.Busy {
		return
	}
	b.Loading = true
	sequence := b.Sequence
	client, parent := b.Client, a.libraryContext
	go func() {
		ctx, cancel := context.WithTimeout(parent, 15*time.Second)
		defer cancel()
		items, err := client.List(ctx)
		restores, restoreErr := client.Restores(ctx)
		if parent.Err() != nil {
			return
		}
		a.dispatch(func() {
			if sequence != b.Sequence {
				return
			}
			b.Loading = false
			if restoreErr != nil {
				b.RestoreCatalogueError = "恢复记录读取失败或存在未完成项；已有恢复文件保留。"
			} else {
				b.RestoreCatalogueError = ""
				for _, r := range restores {
					if r.State == "recovery_required" {
						b.RestoreLocked = true
						b.RestoreOperation = r.ID
						b.Notice = "上次恢复尚未完成，请沿用原恢复记录再次确认；服务需保持停止。"
						for _, item := range items {
							if item.ID == r.Backup {
								b.RestoreSelection = item
							}
						}
						break
					} else if b.LastRestore.ID == "" {
						b.LastRestore = r
						// Catalogue history is not a new completion event.
						a.maintenanceNext.DismissedRestore = r.ID
					}
				}
			}
			if err != nil {
				b.CatalogueError = "备份列表读取失败，已有文件保留；请检查数据目录。"
				return
			}
			b.CatalogueError = ""
			b.Loaded = true
			b.Items = items
			selectedFound := false
			for _, item := range items {
				if item.ID == b.SelectedID {
					selectedFound = true
					break
				}
			}
			if !selectedFound {
				b.SelectedID = ""
				if len(items) > 0 {
					b.SelectedID = items[0].ID
				}
			}
			b.Offset = min(b.Offset, max(0, len(items)-1))
		})
	}()
}
func (a *App) createBackup() {
	b := &a.backups
	if a.libraryContext == nil || b.Busy {
		return
	}
	if b.Operation == "" {
		b.Operation = uuid.NewString()
	}
	id := b.Operation
	b.Busy = true
	b.Creating = true
	b.Sequence++
	b.Loading = false
	sequence, client, parent := b.Sequence, b.Client, a.libraryContext
	b.Error = ""
	b.Notice = "正在创建一致性备份…"
	go func() {
		ctx, cancel := context.WithTimeout(parent, 65*time.Second)
		defer cancel()
		receipt, err := client.Create(ctx, id)
		if parent.Err() != nil {
			return
		}
		a.dispatch(func() {
			if sequence != b.Sequence {
				return
			}
			if err != nil && b.Cancelling {
				return
			}
			b.Busy, b.Creating = false, false
			if err != nil {
				b.Notice = ""
				b.Error = "备份提交失败，原文和已有备份保留；重试沿用同一备份 ID。"
				if a.noteMaintenanceStorageError("backup", id, id, err) {
					b.Error = maintenanceStorageMessage(serviceipc.StorageFailureCode(err)) + "重试沿用原备份 ID。"
				}
				var failure *serviceipc.ServiceError
				if errors.As(err, &failure) {
					switch {
					case strings.Contains(failure.Message, "exceeds 64 MiB"):
						b.Error = "数据库超过 64 MiB，当前在线备份无法完成；请使用经验证的离线备份方式。"
					case failure.Message == "backup is already running":
						b.Error = "已有备份正在执行，请稍后重试。"
					case strings.Contains(failure.Message, "checksum") || strings.Contains(failure.Message, "receipt"):
						b.Error = "同 ID 备份校验失败，已有文件保留；请检查备份目录。"
					}
				}
				return
			}
			a.clearMaintenanceStorageError("backup", id)
			b.Operation = ""
			b.Offset = 0
			b.VerifiedID = receipt.ID
			b.VerifiedAt = time.Now().Local().Format("2006-01-02 15:04:05")
			b.Notice = "一致性备份已完成 · " + receipt.ID
			b.Error = ""
			a.refreshBackups()
		})
	}()
}

func (a *App) cancelBackup() {
	b := &a.backups
	if !b.Creating || b.Cancelling || b.Operation == "" || a.libraryContext == nil {
		return
	}
	b.Cancelling = true
	b.Notice = "正在取消备份，等待清理回执…"
	sequence, id, parent, client := b.Sequence, b.Operation, a.libraryContext, b.Client
	go func() {
		ctx, cancel := context.WithTimeout(parent, 65*time.Second)
		defer cancel()
		result, err := client.Cancel(ctx, id)
		if parent.Err() != nil {
			return
		}
		a.dispatch(func() {
			if sequence != b.Sequence {
				return
			}
			b.Cancelling = false
			if err != nil {
				b.Error = "取消尚未确认，请核对备份记录后重试：" + err.Error()
				b.Notice = ""
				return
			}
			b.Busy, b.Creating = false, false
			// The terminal cancellation reply owns this operation's publication
			// outcome; a delayed Create reply must not overwrite it.
			b.Sequence++
			b.Loading = false
			b.Error = ""
			switch result.State {
			case "cancelled":
				b.Operation = ""
				b.Notice = "备份已取消，未发布成功快照。"
			case "completed":
				b.Operation = ""
				b.Notice = "备份已完成，取消请求到达前已发布快照。"
				if result.Receipt != nil {
					b.VerifiedID = result.Receipt.ID
					b.VerifiedAt = time.Now().Local().Format("2006-01-02 15:04:05")
				}
			default:
				b.Error = "备份未完成，已有文件保留；请重试。"
			}
			a.refreshBackups()
		})
	}()
}
func (a *App) verifyBackup(id string) {
	b := &a.backups
	if a.libraryContext == nil || b.Busy {
		return
	}
	b.Busy = true
	b.Sequence++
	b.Loading = false
	sequence, client, parent := b.Sequence, b.Client, a.libraryContext
	b.Error = ""
	b.Notice = "正在校验备份文件…"
	go func() {
		ctx, cancel := context.WithTimeout(parent, 30*time.Second)
		defer cancel()
		receipt, err := client.Verify(ctx, id)
		if parent.Err() != nil {
			return
		}
		a.dispatch(func() {
			if sequence != b.Sequence {
				return
			}
			b.Busy = false
			b.Notice = ""
			if err != nil {
				if b.VerifiedID == id {
					b.VerifiedID = ""
					b.VerifiedAt = ""
				}
				b.Error = "备份文件校验失败，不能用于恢复；原文件保留。"
				return
			}
			b.VerifiedID = receipt.ID
			b.VerifiedAt = time.Now().Local().Format("2006-01-02 15:04:05")
			b.Notice = "备份文件校验通过 · " + receipt.ID
		})
	}()
}
