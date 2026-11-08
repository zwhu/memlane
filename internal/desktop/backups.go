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
	Client                                                           serviceipc.BackupClient
	Items                                                            []records.BackupReceipt
	Busy, Loading, Loaded                                            bool
	Offset                                                           int
	Operation, Notice, Error, CatalogueError, VerifiedID, VerifiedAt string
	Sequence                                                         int64
}

func (a *App) backupSettings(c *ui.Context) {
	b := &a.backups
	ui.Divider(c)
	ui.Text(c, "一致性备份").Bold()
	ui.Text(c, "备份已保存的原文、来源、版本与项目记录。引擎、模型和代码仓库单独管理；在线备份上限 64 MiB。").TextColor(c.Theme().TextMuted)
	quietButton(c, "创建一致性备份").Key("backup-create").Disabled(a.libraryContext == nil || b.Busy).OnClick(a.createBackup)
	if b.Notice != "" {
		ui.Text(c, b.Notice).Selectable()
	}
	if b.Error != "" {
		ui.Text(c, b.Error).Selectable()
	}
	if b.CatalogueError != "" {
		ui.Text(c, b.CatalogueError).Selectable()
	}
	if len(b.Items) == 0 && b.Loaded && !b.Loading && b.CatalogueError == "" {
		ui.Text(c, "尚无手动备份。").TextColor(c.Theme().TextMuted)
	}
	if !b.Loaded && b.CatalogueError == "" {
		ui.Text(c, "尚未读取备份记录。").TextColor(c.Theme().TextMuted)
	}
	for _, r := range b.Items[min(b.Offset, len(b.Items)):min(b.Offset+10, len(b.Items))] {
		if r.Verification == "invalid" {
			ui.Text(c, "备份记录损坏或不可读，不能用于恢复。").FontSize(11)
		} else {
			ui.Text(c, fmt.Sprintf("%s · schema %d · %.2f MB", r.CreatedAt, r.Schema, float64(r.Bytes)/1e6)).FontSize(11).Selectable()
		}
		ui.Text(c, "备份 · "+r.ID).FontSize(11).Selectable()
		quietButton(c, "校验此备份").Key("backup-verify-" + r.ID).Disabled(b.Busy).OnClick(func() { a.verifyBackup(r.ID) })
		if b.VerifiedID == r.ID {
			ui.Text(c, "上次校验通过 · "+b.VerifiedAt).FontSize(11).TextColor(c.Theme().TextMuted)
		} else {
			ui.Text(c, "当前文件待校验").FontSize(11).TextColor(c.Theme().TextMuted)
		}
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
		if parent.Err() != nil {
			return
		}
		a.dispatch(func() {
			if sequence != b.Sequence {
				return
			}
			b.Loading = false
			if err != nil {
				b.CatalogueError = "备份列表读取失败，已有文件保留；请检查数据目录。"
				return
			}
			b.CatalogueError = ""
			b.Loaded = true
			b.Items = items
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
			b.Busy = false
			if err != nil {
				b.Notice = ""
				b.Error = "备份提交失败，原文和已有备份保留；重试沿用同一备份 ID。"
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
