package simpledesktop

import (
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"
)

func (a *App) projectPermissionCard(c *ui.Context, p AccessProject, agent AccessAgent) {
	key := p.ID + "/" + agent.ID
	actual := p.Overrides[agent.ID].effective(a.access.Directory.Defaults)
	savedRecently := a.access.CheckboxPending[key] == nil && time.Now().Before(a.access.PermissionSaved[key])
	v4Row(c, v4RowOptions{
		Key:   "permission-row-" + key,
		Title: agentDisplayName(agent.Name),
		Desc:  accessTierDesc(actual.Access),
		Leading: func() {
			agentLogo(c, accessAgentIcon(agent), 20)
		},
		Control: func() {
			if savedRecently {
				homeText(c, "已保存", homeMeta).TextColor(tok.OK).SingleLine()
			}
			a.agentPolicyEditor(c, p, agent)
		},
	})
}

func (a *App) projectQueueSection(c *ui.Context, p AccessProject) {
	q := a.projectQueue(p)
	setupOnly := a.isSetupOnlyRuntime()
	enabled := q.projectAutoCapture(p)
	controls := func() {
		v4Switch(c, "proj-autosave-"+p.ID, &enabled, "自动采集", func() {
			a.changeProjectAutoCapture(q, p, enabled)
		}).Label("自动采集").Disabled(q.AutoCaptureBusy || a.access.Busy || a.Config.ProjectControl == nil || a.Update == nil)
		v4SmallButton(c, "刷新", v4IconRefresh, func() {
			a.loadProjectQueue(q, p)
		}).Disabled(q.Loading || a.access.Busy)
	}
	failed := q.InboxFailed > 0 || q.LastError != ""
	healthy := !setupOnly && q.Loaded && !failed && q.InboxPending == 0 && max(q.PendingDocumentsCount, len(q.Documents)) == 0 &&
		q.DocumentError == "" && q.AutoCaptureError == "" && !p.policySyncUnconfirmed()
	var actions func()
	if !setupOnly && !healthy {
		actions = controls
	}
	section := v4Group(c.Key("project-queue-section-"+p.ID), "消息与采集状态", "", actions, func() {
		if setupOnly {
			v4Row(c, v4RowOptions{Key: "proj-queue-setup-" + p.ID, Title: "待配置记忆提炼",
				Desc:    "完成记忆提炼配置后，将自动整理该项目的会话记忆。",
				Control: func() { v4SmallButton(c, "立即配置", nil, a.openSettingsFromDeferredBanner) },
			})
			return
		}
		if healthy {
			title, tone := "自动采集已关闭", "neutral"
			if enabled {
				title, tone = "自动采集已开启 · 正常", "ok"
			}
			v4Row(c, v4RowOptions{Key: "proj-msg-healthy-" + p.ID, Title: title, Leading: func() { v4StatusDot(c, tone) }, Control: controls})
			return
		}
		if p.policySyncUnconfirmed() {
			v4Row(c, v4RowOptions{Key: "proj-policy-sync-" + p.ID, Title: "项目权限同步待确认",
				Leading: func() { v4StatusDot(c, "warn") },
				Control: func() {
					v4SmallButton(c, "重新同步", nil, func() { a.reconcileProjectPolicy(q, p) }).Disabled(a.access.Busy)
				},
			})
		}
		if failed {
			title, reason := "最近采集失败", q.LastError
			if q.InboxFailed > 0 {
				title = fmt.Sprintf("采集队列有 %d 项失败", q.InboxFailed)
			}
			if reason == "" {
				reason = "最近一次采集未能完成，可重试失败记录。"
			}
			v4Row(c, v4RowOptions{Key: "proj-inbox-failure-" + p.ID, Title: title, Desc: reason, Error: q.InboxRetryError,
				Leading: func() { v4StatusDot(c, "fail") },
				Control: func() {
					v4SmallPrimaryButton(c, "一键重试", func() { a.retryProjectInbox(q, p) }).
						Disabled(q.InboxRetryBusy || a.access.Busy || a.Config.ProjectControl == nil || a.Update == nil)
				},
			})
		}
		if q.InboxPending > 0 {
			v4Row(c, v4RowOptions{Key: "proj-inbox-pending-" + p.ID, Title: fmt.Sprintf("有 %d 条原始记录等待提炼", q.InboxPending),
				Leading: func() { v4StatusDot(c, "run") },
			})
		}
		if count := max(q.PendingDocumentsCount, len(q.Documents)); q.Loaded && q.DocumentError == "" && count > 0 {
			v4Row(c, v4RowOptions{Key: "proj-doc-pending-" + p.ID, Title: fmt.Sprintf("有 %d 份文档等待提炼", count),
				Leading: func() { v4StatusDot(c, "run") },
			})
		}
		for _, message := range []string{q.DocumentError, q.AutoCaptureError} {
			if message != "" {
				homeText(c, message, homeMeta).TextColor(tok.Danger)
			}
		}
		if !q.Loaded && !failed && q.InboxPending == 0 {
			v4SkeletonRow(c, 1, false, false)
		}
	})
	if a.access.RevealQueueProject == p.ID {
		section.ScrollIntoView()
		a.access.RevealQueueProject = ""
	}
}
