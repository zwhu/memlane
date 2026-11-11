package desktop

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
)

func (a *App) indexRecoverySettings(c *ui.Context) {
	a.indexRecoveryBody(c)
	a.indexRecoveryActions(c)
}

func (a *App) indexRecoveryPage(c *ui.Context) {
	ui.Text(c, "重建检索索引").FontSize(design.Small).TextColor(design.Muted).Padding(20, 28).Shrink(0).BorderWidth(0, 0, 1, 0).BorderColor(design.Border)
	ui.Scroll(c.Key("maintenance-index-body")).Grow(1).MinHeight(0).Padding(28).Gap(20).Children(func() { a.indexRecoveryBody(c) })
	ui.Row(c).Key("maintenance-index-footer").MinHeight(64).Shrink(0).Padding(14, 24).Gap(10).AlignItems(ui.Center).BorderWidth(1, 0, 0, 0).BorderColor(design.Border).Children(func() {
		ui.Text(c, "正文与版本保持不变").FontSize(10).TextColor(design.Muted).Grow(1).MinWidth(0)
		a.indexRecoveryActions(c)
	})
}

func (a *App) indexRecoveryBody(c *ui.Context) {
	ui.Column(c).Gap(8).Children(func() {
		ui.Text(c, "恢复检索能力").FontSize(design.Small).TextColor(design.Muted)
		ui.Text(c, "已有正文与版本保持").FontSize(design.Heading).Bold()
		ui.Text(c, "重建索引只恢复检索，不新增知识版本。").TextColor(design.Muted)
	})
	if a.runtimeStatus.Phase == "stopped" {
		ui.Text(c, "服务已主动停止，请明确启动服务后再重建索引。").TextColor(design.Muted)
	}
	if a.indexRebuildScope == "" {
		a.indexRebuildScope = "全部本机范围"
	}
	labels := []string{"全部本机范围"}
	for _, s := range a.scopes {
		labels = append(labels, scopeDisplayName(s)+" · "+s.ID[:min(8, len(s.ID))])
	}
	ui.Column(c).Gap(8).Children(func() {
		ui.Text(c, "重建范围").FontSize(design.Small).TextColor(design.Muted)
		priorScope := a.indexRebuildScope
		ui.Select(c.Key("index-rebuild-scope"), &a.indexRebuildScope, labels).Disabled(a.indexRebuildBusy || a.indexRebuild.State == "running")
		if priorScope != a.indexRebuildScope {
			a.indexRebuildOperation = ""
			a.indexRebuildConfirm = false
		}
	})
	if a.indexRebuildConfirm {
		ui.Text(c, "确认后暂时移除所选范围的检索绑定，再逐条重建当前有效版本。归档、删除和失效摘要不会恢复为有效知识。").TextColor(c.Theme().TextMuted)
	}
	if a.indexRebuildError != "" {
		ui.Text(c, a.indexRebuildError).Selectable()
	}
	if r := a.indexRebuild; r.ID != "" {
		labels := map[string]string{"running": "正在重建", "completed": "重建完成", "partial_failed": "部分失败", "failed": "重建失败", "expired": "来源版本已变化", "cancelled": "重建已取消，已完成条目保留"}
		ui.Text(c, labels[r.State]).Key("index-rebuild-state").Bold()
		a.maintenanceTechnicalDetails(c, "index:"+r.ID, func() {
			ui.Text(c, "任务 · "+r.ID).FontSize(11).Selectable()
		})
		counts := map[string]int{}
		for _, item := range r.Items {
			if item.Error == "rebuild_cancelled" {
				counts["cancelled"]++
			} else {
				counts[item.State]++
			}
		}
		ui.Text(c, fmt.Sprintf("共 %d 条 · 完成 %d · 等待 %d · 失败 %d · 过期 %d · 已取消 %d", len(r.Items), counts["completed"], counts["queued"], counts["failed"], counts["expired"], counts["cancelled"]))
		shown := 0
		for _, item := range r.Items {
			if item.Error == "" {
				continue
			}
			if shown == 5 {
				ui.Text(c, "其余错误保留在任务记录中。").TextColor(c.Theme().TextMuted)
				break
			}
			message := "最近索引错误：" + item.Error
			if item.Error == "rebuild_cancelled" {
				message = "已取消，可重试此版本"
			}
			ui.Text(c, fmt.Sprintf("%s · v%d · %s", item.Memory[:8], item.Revision, message)).FontSize(11).Selectable()
			shown++
		}
	}
}

func (a *App) indexRecoveryActions(c *ui.Context) {
	if a.indexRebuild.State == "running" {
		actionButton(c, "取消未完成重建", nil).Key("index-rebuild-cancel").Disabled(a.indexRebuildBusy).OnClick(a.cancelIndexRebuild)
		return
	}
	if a.indexRebuildConfirm {
		quietButton(c, "取消").Key("index-rebuild-dismiss").Disabled(a.indexRebuildBusy).OnClick(func() { a.indexRebuildConfirm = false; a.indexRebuildError = ""; a.indexRebuildOperation = "" })
		actionButton(c, "确认重建", nil).Key("index-rebuild-confirm").Disabled(a.indexRebuildBusy || a.runtimeStatus.Phase == "stopped").OnClick(a.startIndexRebuild)
		return
	}
	if a.indexRebuild.ID != "" && a.indexRebuild.State != "completed" {
		quietButton(c, "重建其他范围").Key("index-rebuild-other-scope").Disabled(a.indexRebuildBusy).OnClick(func() { a.indexRebuildConfirm = true; a.indexRebuildError = ""; a.indexRebuildOperation = "" })
		actionButton(c, "重试未完成项", nil).Key("index-rebuild-retry").Disabled(a.indexRebuildBusy || a.runtimeStatus.Phase == "stopped").OnClick(a.retryIndexRebuild)
		return
	}
	actionButton(c, "重建检索索引", nil).Key("index-rebuild-open").Disabled(a.libraryContext == nil || a.indexRebuildBusy).OnClick(func() { a.indexRebuildConfirm = true; a.indexRebuildError = ""; a.indexRebuildOperation = "" })
}

func (a *App) startIndexRebuild() {
	if a.libraryContext == nil || a.indexRebuildBusy || !a.indexRebuildConfirm || a.runtimeStatus.Phase == "stopped" {
		return
	}
	scope := ""
	if a.indexRebuildScope != "全部本机范围" {
		for _, s := range a.scopes {
			if a.indexRebuildScope == scopeDisplayName(s)+" · "+s.ID[:min(8, len(s.ID))] {
				scope = s.ID
			}
		}
		if scope == "" {
			a.indexRebuildError = "请选择当前已登记的范围。"
			return
		}
	}
	if a.indexRebuildOperation == "" {
		a.indexRebuildOperation = uuid.NewString()
	}
	start := records.IndexRebuildStart{OperationID: a.indexRebuildOperation, Scope: scope}
	a.submitIndexRebuild(start)
}

func (a *App) retryIndexRebuild() {
	if a.libraryContext == nil || a.indexRebuildBusy || a.indexRebuild.ID == "" || a.runtimeStatus.Phase == "stopped" {
		return
	}
	if a.indexRebuildOperation == "" {
		a.indexRebuildOperation = uuid.NewString()
	}
	a.submitIndexRebuild(records.IndexRebuildStart{OperationID: a.indexRebuildOperation, RetryOf: a.indexRebuild.ID})
}

func (a *App) submitIndexRebuild(start records.IndexRebuildStart) {
	a.indexRebuildSequence++
	a.indexRebuildBusy = true
	sequence, parent, library := a.indexRebuildSequence, a.libraryContext, a.library
	go func() {
		ctx, cancel := context.WithTimeout(parent, 15*time.Second)
		defer cancel()
		result, err := library.RebuildIndex(ctx, start)
		if parent.Err() != nil {
			return
		}
		a.dispatch(func() {
			if sequence != a.indexRebuildSequence || a.library.Root != library.Root {
				return
			}
			a.indexRebuildBusy = false
			if err != nil {
				a.indexRebuildError = "重建提交失败，正文仍已保存；重试会沿用同一任务。"
				var failure *serviceipc.ServiceError
				if errors.As(err, &failure) {
					switch {
					case failure.Message == "rebuild exceeds 10000 items; choose a scope":
						a.indexRebuildError = "所选范围超过 10000 条，请选择一个较小的项目或通用知识范围后重建。"
					case failure.Code == "not_found":
						a.indexRebuildError = "所选范围已不可用，请重新选择当前已登记的范围。"
					case failure.Code == "conflict":
						a.indexRebuildError = "该任务已绑定其他范围，请取消后重新确认。"
					}
				}
				return
			}
			a.indexRebuild = result
			a.indexRebuildConfirm = false
			a.indexRebuildError = ""
			a.indexRebuildOperation = ""
			a.refresh(false)
		})
	}()
}

func (a *App) cancelIndexRebuild() {
	if a.libraryContext == nil || a.indexRebuildBusy || a.indexRebuild.ID == "" || a.indexRebuild.State != "running" {
		return
	}
	a.indexRebuildBusy = true
	a.indexRebuildSequence++
	sequence, id, parent, library := a.indexRebuildSequence, a.indexRebuild.ID, a.libraryContext, a.library
	go func() {
		ctx, cancel := context.WithTimeout(parent, 15*time.Second)
		defer cancel()
		result, err := library.CancelIndexRebuild(ctx, id)
		if parent.Err() != nil {
			return
		}
		a.dispatch(func() {
			if sequence != a.indexRebuildSequence {
				return
			}
			a.indexRebuildBusy = false
			if err != nil {
				a.indexRebuildError = "取消尚未确认，已完成条目保留；请重试。"
				return
			}
			a.indexRebuild, a.indexRebuildError, a.indexRebuildOperation = result, "", ""
			a.refresh(false)
		})
	}()
}

func (a *App) refreshIndexRebuild() {
	if a.libraryContext == nil || a.indexRebuildLoading || a.indexRebuildBusy {
		return
	}
	a.indexRebuildLoading = true
	sequence, parent, library := a.indexRebuildSequence, a.libraryContext, a.library
	go func() {
		ctx, cancel := context.WithTimeout(parent, 5*time.Second)
		defer cancel()
		result, err := library.IndexRebuild(ctx, "")
		if parent.Err() != nil {
			return
		}
		a.dispatch(func() {
			if a.library.Root != library.Root {
				return
			}
			a.indexRebuildLoading = false
			if err == nil && sequence == a.indexRebuildSequence {
				a.indexRebuild = result
			}
		})
	}()
}
