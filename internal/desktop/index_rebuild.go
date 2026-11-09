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
	ui.Text(c, "数据与检索恢复").Bold()
	ui.Text(c, "索引损坏或丢失时，从已保存的当前原文重建检索。正文、来源和历史保留。").TextColor(c.Theme().TextMuted)
	if a.indexRebuildScope == "" {
		a.indexRebuildScope = "全部本机范围"
	}
	labels := []string{"全部本机范围"}
	for _, s := range a.scopes {
		labels = append(labels, s.Name+" · "+s.ID[:8])
	}
	if !a.indexRebuildConfirm {
		quietButton(c, "重建检索索引").Key("index-rebuild-open").Disabled(a.libraryContext == nil || a.indexRebuildBusy).OnClick(func() { a.indexRebuildConfirm = true; a.indexRebuildError = ""; a.indexRebuildOperation = "" })
	} else {
		ui.Text(c, "确认后暂时移除所选范围的检索绑定，再逐条重建当前有效版本。归档、删除和失效摘要不会恢复为有效知识。").TextColor(c.Theme().TextMuted)
		priorScope := a.indexRebuildScope
		ui.Select(c.Key("index-rebuild-scope"), &a.indexRebuildScope, labels).Disabled(a.indexRebuildBusy)
		if priorScope != a.indexRebuildScope {
			a.indexRebuildOperation = ""
		}
		ui.Row(c).Gap(8).Children(func() {
			quietButton(c, "确认重建").Key("index-rebuild-confirm").Disabled(a.indexRebuildBusy).OnClick(a.startIndexRebuild)
			quietButton(c, "取消").Key("index-rebuild-dismiss").Disabled(a.indexRebuildBusy).OnClick(func() { a.indexRebuildConfirm = false; a.indexRebuildError = ""; a.indexRebuildOperation = "" })
		})
	}
	if a.indexRebuildError != "" {
		ui.Text(c, a.indexRebuildError).Selectable()
	}
	if r := a.indexRebuild; r.ID != "" {
		labels := map[string]string{"running": "正在重建", "completed": "重建完成", "partial_failed": "部分失败", "failed": "重建失败", "expired": "来源版本已变化"}
		ui.Text(c, labels[r.State]).Key("index-rebuild-state").Bold()
		ui.Text(c, "任务 · "+r.ID).FontSize(11).Selectable()
		counts := map[string]int{}
		for _, item := range r.Items {
			counts[item.State]++
		}
		ui.Text(c, fmt.Sprintf("共 %d 条 · 完成 %d · 等待 %d · 失败 %d · 过期 %d", len(r.Items), counts["completed"], counts["queued"], counts["failed"], counts["expired"]))
		shown := 0
		for _, item := range r.Items {
			if item.Error == "" {
				continue
			}
			if shown == 5 {
				ui.Text(c, "其余错误保留在任务记录中。").TextColor(c.Theme().TextMuted)
				break
			}
			ui.Text(c, fmt.Sprintf("%s · v%d · 最近索引错误：%s", item.Memory[:8], item.Revision, item.Error)).FontSize(11).Selectable()
			shown++
		}
	}
}

func (a *App) startIndexRebuild() {
	if a.libraryContext == nil || a.indexRebuildBusy || !a.indexRebuildConfirm {
		return
	}
	scope := ""
	if a.indexRebuildScope != "全部本机范围" {
		for _, s := range a.scopes {
			if a.indexRebuildScope == s.Name+" · "+s.ID[:8] {
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
	a.indexRebuildSequence++
	a.indexRebuildBusy = true
	go func() {
		ctx, cancel := context.WithTimeout(a.libraryContext, 15*time.Second)
		defer cancel()
		result, err := a.library.RebuildIndex(ctx, start)
		if a.libraryContext.Err() != nil {
			return
		}
		a.dispatch(func() {
			a.indexRebuildBusy = false
			if err != nil {
				a.indexRebuildError = "重建提交失败，正文仍已保存；重试会沿用同一任务。"
				var failure *serviceipc.ServiceError
				if errors.As(err, &failure) {
					switch {
					case failure.Message == "rebuild exceeds 10000 items; choose a scope":
						a.indexRebuildError = "所选范围超过 10000 条，请选择一个较小的项目或全局范围后重建。"
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

func (a *App) refreshIndexRebuild() {
	if a.libraryContext == nil || a.indexRebuildLoading || a.indexRebuildBusy {
		return
	}
	a.indexRebuildLoading = true
	sequence := a.indexRebuildSequence
	go func() {
		ctx, cancel := context.WithTimeout(a.libraryContext, 5*time.Second)
		defer cancel()
		result, err := a.library.IndexRebuild(ctx, "")
		if a.libraryContext.Err() != nil {
			return
		}
		a.dispatch(func() {
			a.indexRebuildLoading = false
			if err == nil && sequence == a.indexRebuildSequence {
				a.indexRebuild = result
			}
		})
	}()
}
