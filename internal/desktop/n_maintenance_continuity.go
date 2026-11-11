package desktop

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/serviceipc"
)

type MaintenanceNextUI struct {
	WaitingOwner                   string
	WaitingGeneration              int64
	Observe                        func(context.Context) (RuntimeAvailability, error)
	OpenSettings                   func() error
	Reopen                         func()
	Recovery                       string
	Attempts, Limit, WindowSeconds int
	Busy, Awaiting                 bool
	Notice                         string
	DismissedRestore               string
	RestoreDetailsOpen             bool
	Storage                        *MaintenanceStorageFault
}
type MaintenanceStorageFault struct {
	Kind, Owner, Operation, Page, Scope, Code string
	Epoch                                     uint64
}

func (a *App) ConfigureMaintenanceRecovery(observe func(context.Context) (RuntimeAvailability, error), openSettings func() error, reopen func()) {
	a.maintenanceNext.Observe, a.maintenanceNext.OpenSettings, a.maintenanceNext.Reopen = observe, openSettings, reopen
}

func (a *App) retireMaintenanceRecoveryState() {
	n := a.maintenanceNext
	a.maintenanceNext = MaintenanceNextUI{Observe: n.Observe, OpenSettings: n.OpenSettings, Reopen: n.Reopen}
}
func (a *App) maintenanceRecoveryNotice(c *ui.Context) {
	n := &a.maintenanceNext
	if a.runtimeStatus.Phase == "authorization" {
		ui.Column(c).Padding(12).Gap(8).Border(1, design.Border).Radius(6).Children(func() {
			ui.Text(c, "后台权限需要恢复").Bold().FontSize(12)
			ui.Text(c, "请在系统设置的登录项与扩展中允许 Memlane。现有知识与草稿保留；打开设置不等于恢复许可。").FontSize(11).LineHeight(1.6)
			ui.Row(c).Wrap().Gap(8).Children(func() {
				quietButton(c, "查看系统设置入口").Key("maintenance-permission-settings").Disabled(n.OpenSettings == nil || n.Busy).OnClick(func() {
					if err := n.OpenSettings(); err != nil {
						n.Notice = "系统设置尚未打开，请自行打开登录项与扩展。"
					} else {
						n.Notice = "已请求打开系统设置；恢复许可后请重新验证。"
					}
				})
				quietButton(c, "恢复许可后重新验证").Key("maintenance-permission-recheck").Disabled(n.Observe == nil || n.Busy || a.libraryContext == nil).OnClick(a.recheckMaintenancePermission)
			})
		})
	} else if n.Recovery == "exhausted" && a.serviceState == "failed" {
		ui.Column(c).Padding(12).Gap(8).Border(1, design.Border).Radius(6).Children(func() {
			ui.Text(c, "服务恢复失败").Bold()
			if n.Limit > 0 && n.WindowSeconds > 0 {
				ui.Text(c, fmt.Sprintf("原文处理组件在最近 %d 分钟内已恢复失败 %d 次，达到 %d 次自动恢复上限。请处理原因后明确重试。", n.WindowSeconds/60, n.Attempts, n.Limit)).FontSize(11).LineHeight(1.6)
			} else {
				ui.Text(c, "原文处理组件已停止自动恢复，请查看诊断后明确重试。").FontSize(11)
			}
		})
	} else if n.Recovery == "recovering" && (a.serviceState == "recovering" || a.serviceState == "preparing") {
		ui.Text(c, "原文处理组件正在按有限恢复规则重试；完成状态以实际服务回执为准。").FontSize(11)
	}
	if n.Busy {
		ui.Text(c, "正在核对实际服务回执…").FontSize(11)
	}
	if n.Notice != "" {
		ui.Text(c, n.Notice).FontSize(11).LineHeight(1.6)
	}
}
func (a *App) retryMaintenanceService() {
	n := &a.maintenanceNext
	if n.Busy || n.Awaiting || a.libraryContext == nil || a.dispatch == nil || a.runtimeStatus.Phase == "stopped" || a.runtimeStatus.Phase == "authorization" || a.serviceState != "failed" {
		return
	}
	n.Busy = true
	n.Notice = "正在提交明确重试请求…"
	life, client, dispatch, epoch := a.libraryContext, a.library, a.dispatch, a.libraryEpoch
	go func() {
		ctx, cancel := context.WithTimeout(life, 8*time.Second)
		defer cancel()
		var receipt struct {
			Requested  bool   `json:"retry_requested"`
			Owner      string `json:"recovery_owner"`
			Generation int64  `json:"recovery_generation"`
		}
		err := serviceipc.Call(ctx, client.Root, serviceipc.Request{Op: "retry", UserConfirmed: true}, &receipt)
		if life.Err() != nil {
			return
		}
		dispatch(func() {
			if a.libraryEpoch != epoch {
				return
			}
			n.Busy = false
			if err != nil || !receipt.Requested {
				n.Notice = "重试请求未确认，服务状态保留；请查看诊断后再试。"
				return
			}
			n.Awaiting = true
			n.WaitingOwner, n.WaitingGeneration = receipt.Owner, receipt.Generation
			n.Notice = "已提交重试，等待后台实际恢复回执；尚未确认可用。"
		})
	}()
}
func (a *App) recheckMaintenancePermission() {
	n := &a.maintenanceNext
	if n.Busy || n.Observe == nil || a.libraryContext == nil || a.dispatch == nil || a.runtimeStatus.Phase == "stopped" {
		return
	}
	n.Busy = true
	observe := n.Observe
	life, dispatch, epoch := a.libraryContext, a.dispatch, a.libraryEpoch
	go func() {
		ctx, cancel := context.WithTimeout(life, 4*time.Second)
		defer cancel()
		status, err := observe(ctx)
		if life.Err() != nil {
			return
		}
		dispatch(func() {
			if a.libraryEpoch != epoch {
				return
			}
			n.Busy = false
			if err != nil {
				n.Notice = "后台许可状态未确认，未注册或启动服务；请稍后重新验证。"
				return
			}
			if status.LibraryRoot != "" && status.LibraryRoot != a.library.Root {
				n.Notice = "当前知识库的后台状态尚未核验，请重新读取当前库的状态。"
				return
			}
			a.ApplyRuntimeAvailability(status)
			if status.Phase == "authorization" {
				n.Notice = "后台许可仍需系统批准，请在设置中允许后再验证。"
			} else if status.Phase == "ready" {
				n.Notice = "系统许可与服务就绪状态已取得实际回执。"
			} else {
				n.Notice = "已读取当前许可与服务状态；服务尚未就绪，没有自动启动。"
			}
		})
	}()
}
func (a *App) maintenanceRestoreSummary(c *ui.Context) bool {
	b := &a.backups
	n := &a.maintenanceNext
	r := b.LastRestore
	if r.ID == "" || r.State != "restored_stopped" || r.ID == n.DismissedRestore || b.RestoreSelection.ID != "" {
		return false
	}
	ui.Column(c).Grow(1).MinHeight(0).Children(func() {
		ui.Row(c).Shrink(0).Padding(18, 24).Children(func() {
			ui.Text(c, "恢复完成").FontSize(12)
			ui.Spacer(c)
			quietButton(c, "查看备份列表").OnClick(func() { n.DismissedRestore = r.ID })
		})
		ui.Scroll(c).Grow(1).MinHeight(0).Padding(28).Gap(18).Children(func() {
			title := "知识已恢复，按当前服务状态继续"
			if a.runtimeStatus.Phase == "stopped" {
				title = "知识已恢复，服务保持停止"
			}
			ui.Text(c, "恢复后的知识库").FontSize(11).TextColor(design.Muted)
			ui.Text(c, title).FontSize(design.Heading).Bold()
			ui.Text(c, "当次恢复已保存恢复前副本，旧授权不会自动重新启用。当前状态以重新核验后的回执为准。").FontSize(12).LineHeight(1.7)
			ui.Column(c).Gap(0).Children(func() {
				for _, row := range [][2]string{{"恢复前副本", "当次恢复已保留"}, {"旧 Agent 连接与计划", fmt.Sprintf("当次退休 %d 个连接、停用 %d 个计划", r.Stats.Connections, r.Stats.Plans)}, {"旧任务和未确认建议", "当次恢复已退休"}, {"原文索引", fmt.Sprintf("当次排队 %d 条，当前进度请查看索引页", r.Stats.Memories)}} {
					ui.Row(c).AlignItems(ui.Start).Gap(12).Padding(12, 0).BorderWidth(0, 0, 1, 0).BorderColor(design.Border).Children(func() {
						ui.Text(c, row[0]).FontSize(11).TextColor(design.Muted).Grow(1).MinWidth(0)
						ui.Text(c, row[1]).FontSize(11).Grow(1).MinWidth(0).Selectable()
					})
				}
			})
			ui.Row(c).Wrap().Gap(8).Children(func() {
				quietButton(c, "明确启动服务").Disabled(a.runtimeStatus.Phase != "stopped" || a.resumeRuntime == nil || a.environmentBusy()).OnClick(func() { a.resumeRuntime() })
				quietButton(c, "重建检索索引").OnClick(func() { a.maintenanceSection = "index"; a.refreshIndexRebuild() })
				quietButton(c, "重新验证 Agent").OnClick(a.openAgentSettings)
			})
			ui.Collapsible(c, "恢复记录详情", &n.RestoreDetailsOpen, func() {
				meta(c, "恢复记录", r.ID)
			})
		})
		ui.Row(c).Shrink(0).Wrap().Padding(14, 24).Gap(10).BorderWidth(1, 0, 0, 0).BorderColor(design.Border).Children(func() {
			ui.Text(c, "不自动恢复旧授权").FontSize(10).Grow(1).MinWidth(0)
			quietButton(c, "重新打开窗口").Disabled(n.Reopen == nil).OnClick(func() { n.Reopen() })
		})
	})
	return true
}
func maintenanceStorageMessage(code string) string {
	switch code {
	case "storage_full":
		return "存储空间、配额或数据库容量已满。写入未完成，原草稿与请求保留；释放空间或调整容量限制后再重试。"
	case "storage_read_only":
		return "存储位置为只读，写入未完成；恢复可写权限后重试，原草稿与请求保留。"
	case "storage_permission":
		return "存储写入权限不足，写入未完成；恢复权限后重试，原草稿与请求保留。"
	}
	return ""
}
func (a *App) manualStorageOwner() string {
	if a.detailMode == "new" {
		return a.newDraftKey()
	}
	return a.selectedID
}
func (a *App) noteMaintenanceStorageError(kind, owner, operation string, err error) bool {
	var remote *serviceipc.ServiceError
	if !errors.As(err, &remote) {
		return false
	}
	return a.noteLocalMaintenanceStorageError(kind, owner, operation, err)
}
func (a *App) noteLocalMaintenanceStorageError(kind, owner, operation string, err error) bool {
	code := serviceipc.StorageFailureCode(err)
	if code == "" {
		return false
	}
	a.maintenanceNext.Storage = &MaintenanceStorageFault{Kind: kind, Owner: owner, Operation: operation, Page: a.page, Scope: a.scope, Code: code, Epoch: a.libraryEpoch}
	return true
}
func (a *App) clearMaintenanceStorageError(kind, owner string) {
	if f := a.maintenanceNext.Storage; f != nil && f.Kind == kind && f.Owner == owner && f.Epoch == a.libraryEpoch {
		a.maintenanceNext.Storage = nil
	}
}
func (a *App) maintenanceStorageNotice(c *ui.Context, kind, owner string) {
	f := a.maintenanceNext.Storage
	if f == nil || f.Epoch != a.libraryEpoch || f.Kind != kind || f.Owner != owner || f.Page != a.page || f.Scope != a.scope {
		return
	}
	ui.Column(c).Key("maintenance-storage-write-failed").Shrink(0).Padding(12).Gap(8).Border(1, design.Border).Radius(6).Children(func() {
		title := "存储暂不可写"
		if f.Code == "storage_full" {
			title = "存储空间不足"
		}
		ui.Text(c, title).Bold().FontSize(12)
		ui.Text(c, maintenanceStorageMessage(f.Code)).FontSize(11).LineHeight(1.6)
		guidance := "处理空间或权限后，请使用当前页面的原请求重试入口继续。"
		switch kind {
		case "manual":
			guidance = "处理空间或权限后，使用下方保存继续；重试沿用原操作标识。"
		case "backup":
			guidance = "处理空间或权限后，使用当前页创建一致性备份重试，沿用原备份 ID。"
		case "restore":
			guidance = "处理空间或权限后，继续确认当前恢复，沿用原恢复 ID。"
		}
		ui.Text(c, guidance).FontSize(11).LineHeight(1.6)
	})
}

func maintenanceRemoteStorageCode(err error) string {
	var remote *serviceipc.ServiceError
	if !errors.As(err, &remote) {
		return ""
	}
	return serviceipc.StorageFailureCode(remote)
}
