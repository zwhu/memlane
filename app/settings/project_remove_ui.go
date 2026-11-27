package settings

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/app/api"
	kitui "github.com/zwhu/memlane/kit/ui"
)

func (a *Controller) projectRemoveSection(c *ui.Context, p AccessProject) {
	tok := a.tokens()
	kitui.Group(c.Key("project-danger-section-"+p.ID), tok, "危险操作", "", nil, func() {
		kitui.Row(c, tok, kitui.RowOptions{
			Key:   "proj-remove-row-" + p.ID,
			Title: "解除项目托管",
			Desc:  "仅解除 Memlane 托管，引擎中已有记忆与已留存原始会话会保留，后续可在「新建项目」中选择已有数据组重新接回。",
			Control: func() {
				kitui.SmallButton(c, tok, "移除项目", nil, func() {
					a.openProjectRemoveDialog(p)
				}).TextColor(tok.Danger).Disabled(a.access.Busy || a.access.RemoveBusy || a.config.ProjectControl == nil || !a.canDispatch())
			},
		})
	})
}

func (a *Controller) clearProjectRemoveDialog() {
	a.access.RemoveTarget = nil
	a.access.RemoveOpen = false
	a.access.RemoveError = ""
	a.access.RemoveSummaryLoading = false
	a.access.RemoveSummaryLoaded = false
	a.access.RemoveSummaryError = ""
	a.access.RemovePending = 0
	a.access.RemoveProcessing = 0
	a.access.RemoveSummaryRequest++
}

func (a *Controller) openProjectRemoveDialog(p AccessProject) {
	if p.IsProtected() || a.access.RemoveBusy {
		return
	}
	target := p
	a.access.RemoveTarget = &target
	a.access.RemoveOpen = true
	a.access.RemoveError = ""
	a.access.RemoveSummaryLoading = false
	a.access.RemoveSummaryLoaded = false
	a.access.RemoveSummaryError = ""
	a.access.RemovePending = 0
	a.access.RemoveProcessing = 0
	a.loadProjectRemoveSummary(target)
}

func (a *Controller) loadProjectRemoveSummary(target AccessProject) {
	if !a.canDispatch() {
		a.access.RemoveSummaryLoading = false
		a.access.RemoveSummaryLoaded = false
		a.access.RemoveSummaryError = "当前窗口未就绪，无法读取待处理任务数"
		return
	}
	client, ok := a.accessClient()
	if !ok {
		a.access.RemoveSummaryLoading = false
		a.access.RemoveSummaryLoaded = false
		a.access.RemoveSummaryError = "项目管理服务不可用，无法读取待处理任务数"
		return
	}
	a.access.RemoveSummaryRequest++
	reqID := a.access.RemoveSummaryRequest
	a.access.RemoveSummaryLoading = true
	a.access.RemoveSummaryLoaded = false
	a.access.RemoveSummaryError = ""
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		var view api.ProjectView
		err := client.request(ctx, "GET", "/v1/projects/"+url.PathEscape(target.ID), nil, &view)
		a.dispatch(func() {
			if reqID != a.access.RemoveSummaryRequest || a.access.RemoveTarget == nil || a.access.RemoveTarget.ID != target.ID {
				return
			}
			a.access.RemoveSummaryLoading = false
			if err != nil {
				msg := ControlUserError(err)
				if msg == "" {
					msg = err.Error()
				}
				a.access.RemoveSummaryError = msg
				a.access.RemoveSummaryLoaded = false
				return
			}
			if view.ID != target.ID || canonicalDatasetKey(view.DatasetID) != canonicalDatasetKey(target.Dataset) {
				a.clearProjectRemoveDialog()
				return
			}
			a.access.RemoveSummaryLoaded = true
			a.access.RemoveSummaryError = ""
			a.access.RemovePending = max(0, max(view.InboxPending, view.Inbox.Pending))
			a.access.RemoveProcessing = max(0, max(view.InboxProcessing, view.Inbox.Processing))
		})
	}()
}

func (a *Controller) projectRemoveModal(c *ui.Context) {
	tok := a.tokens()
	if a.access.RemoveTarget == nil || !a.access.RemoveOpen {
		return
	}
	target := *a.access.RemoveTarget
	displayName := strings.TrimSpace(target.Name)
	if displayName == "" {
		displayName = target.ID
	}
	confirmDisabled := a.access.RemoveBusy || a.access.RemoveSummaryLoading || !a.access.RemoveSummaryLoaded || a.access.RemoveSummaryError != ""
	kitui.Dialog(c, tok, &a.access.RemoveOpen, kitui.DialogOptions{
		Key:             "project-remove-dialog-" + target.ID,
		Title:           fmt.Sprintf("移除项目『%s』？", displayName),
		Width:           kitui.DialogSmall,
		Danger:          true,
		CancelText:      "取消",
		ConfirmText:     "确认移除",
		ConfirmLabel:    "确认移除项目：" + displayName,
		Hint:            "移除后不会删除已有记忆（确认按钮不绑定回车键）",
		CancelDisabled:  a.access.RemoveBusy,
		ConfirmDisabled: confirmDisabled,
		OnCancel: func() {
			if !a.access.RemoveBusy {
				a.clearProjectRemoveDialog()
			}
		},
		OnConfirm: a.confirmRemoveProject,
		Body: func() {
			kitui.Text(c, tok, "移除项目仅解除 Memlane 托管，引擎中已有记忆与已留存原始会话会保留；后续可在「新建项目」中选择已有数据组重新接回。", kitui.RoleBody).
				FontSize(fsBody).TextColor(tok.Text1).LineHeight(1.6)
			switch {
			case a.access.RemoveSummaryLoading:
				kitui.Text(c, tok, "正在获取当前待处理任务数…", kitui.RoleMeta).LineHeight(1.5)
			case a.access.RemoveSummaryError != "":
				ui.Row(c).FillWidth().Gap(sp2).AlignItems(ui.Center).Children(func() {
					kitui.Text(c, tok, "无法获取待处理任务数："+a.access.RemoveSummaryError, kitui.RoleMeta).
						TextColor(tok.Danger).LineHeight(1.5).Grow(1)
					kitui.SmallButton(c, tok, "重试", kitui.IconRefresh, func() {
						a.loadProjectRemoveSummary(target)
					}).Disabled(a.access.RemoveSummaryLoading || a.access.RemoveBusy)
				})
			case a.access.RemoveSummaryLoaded:
				if a.access.RemovePending == 0 && a.access.RemoveProcessing == 0 {
					kitui.Text(c, tok, "当前无待提炼或进行中的任务。", kitui.RoleMeta).LineHeight(1.5)
				}
				if a.access.RemovePending > 0 {
					kitui.Text(c, tok, fmt.Sprintf("待提炼 %d 条：移除后将取消，不再启动提炼。", a.access.RemovePending), kitui.RoleMeta).
						TextColor(tok.Warning).LineHeight(1.5)
				}
				if a.access.RemoveProcessing > 0 {
					kitui.Text(c, tok, fmt.Sprintf("正在提炼 %d 条：已发出给模型的在途请求会自然结束，不会再写入当前项目目录。", a.access.RemoveProcessing), kitui.RoleMeta).
						TextColor(tok.Warning).LineHeight(1.5)
				}
			}
			if a.access.RemoveError != "" {
				kitui.Text(c, tok, a.access.RemoveError, kitui.RoleMeta).TextColor(tok.Danger).LineHeight(1.5)
			}
		},
	})
	if !a.access.RemoveOpen && !a.access.RemoveBusy {
		a.clearProjectRemoveDialog()
	}
}

func canonicalDatasetKey(raw string) string {
	trimmed := strings.TrimSpace(raw)
	trimmed = strings.TrimPrefix(strings.ToLower(trimmed), "urn:uuid:")
	trimmed = strings.Trim(trimmed, "{}")
	return strings.ReplaceAll(trimmed, "-", "")
}

func directoryHasExactProject(projects []AccessProject, id, dataset string) bool {
	wantDS := canonicalDatasetKey(dataset)
	for _, p := range projects {
		if p.ID == id && canonicalDatasetKey(p.Dataset) == wantDS {
			return true
		}
	}
	return false
}

func (a *Controller) confirmRemoveProject() {
	if a.access.RemoveBusy || a.access.RemoveTarget == nil || !a.access.RemoveSummaryLoaded || a.access.RemoveSummaryLoading || a.access.RemoveSummaryError != "" || !a.canDispatch() {
		return
	}
	client, ok := a.accessClient()
	if !ok {
		a.access.RemoveError = "项目管理服务不可用，无法移除项目"
		return
	}
	target := *a.access.RemoveTarget
	if target.IsProtected() {
		a.access.RemoveError = "受保护项目不可移除"
		return
	}
	a.access.RemoveBusy = true
	a.access.RemoveError = ""
	authEpoch := a.authEpoch()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		err := client.deleteProject(ctx, target.ID, target.Dataset)
		var directory AccessDirectory
		var dirErr error
		if err == nil {
			directory, dirErr = client.directory(ctx)
		}
		a.dispatch(func() {
			a.access.RemoveBusy = false
			if authEpoch != a.authEpoch() {
				// Even if a periodic directory refresh advanced authEpoch while DELETE was in flight,
				// close the modal if the authoritative directory no longer contains the exact target (id, dataset).
				if !directoryHasExactProject(a.access.Directory.Projects, target.ID, target.Dataset) ||
					(err == nil && dirErr == nil && !directoryHasExactProject(directory.Projects, target.ID, target.Dataset)) {
					a.clearProjectRemoveDialog()
					if a.access.OpenProject == target.ID && !directoryHasExactProject(a.access.Directory.Projects, target.ID, target.Dataset) {
						a.access.OpenProject = ""
					}
					if err == nil && dirErr == nil {
						a.ApplyAccessDirectory(directory)
					}
				}
				return
			}
			if err != nil {
				msg := ControlUserError(err)
				if msg == "" {
					msg = err.Error()
				}
				a.access.RemoveError = msg
				return
			}
			a.clearProjectRemoveDialog()
			a.invalidateReading(target.Dataset)
			if a.access.OpenProject == target.ID {
				a.access.OpenProject = ""
			}
			if dirErr == nil {
				a.ApplyAccessDirectory(directory)
			} else {
				updated := a.access.Directory
				updated.Projects = slices.DeleteFunc(slices.Clone(updated.Projects), func(p AccessProject) bool {
					return p.ID == target.ID
				})
				a.ApplyAccessDirectory(updated)
				a.RefreshProjectDirectory()
			}
			a.navigateOverview()
		})
	}()
}
