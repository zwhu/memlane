package settings

import (
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"
	kitui "github.com/zwhu/memlane/kit/ui"
)

func (a *Controller) projectPermissionCard(c *ui.Context, p AccessProject, agent AccessAgent) {
	tok := a.tokens()
	key := p.ID + "/" + agent.ID
	actual := p.Overrides[agent.ID].Effective(a.access.Directory.Defaults)
	savedRecently := a.access.CheckboxPending[key] == nil && time.Now().Before(a.access.PermissionSaved[key])
	kitui.Row(c, tok, kitui.RowOptions{
		Key:   "permission-row-" + key,
		Title: AgentDisplayName(agent.Name),
		Desc:  accessTierDesc(actual.Access),
		Leading: func() {
			agentLogo(tok, c, accessAgentIcon(agent), 20)
		},
		Control: func() {
			if savedRecently {
				kitui.Text(c, tok, "已保存", kitui.RoleMeta).TextColor(tok.OK).SingleLine()
			}
			a.agentPolicyEditor(c, p, agent)
		},
	})
}

func (a *Controller) projectQueueSection(c *ui.Context, p AccessProject) {
	tok := a.tokens()
	q := a.projectQueue(p)
	setupOnly := a.isSetupOnlyRuntime()
	enabled := q.projectAutoCapture(p)
	controls := func() {
		kitui.Switch(c, tok, "proj-autosave-"+p.ID, &enabled, "自动采集", func() {
			a.changeProjectAutoCapture(q, p, enabled)
		}).Label("自动采集").Disabled(q.AutoCaptureBusy || a.access.Busy || a.config.ProjectControl == nil || !a.canDispatch())
		kitui.SmallButton(c, tok, "刷新", kitui.IconRefresh, func() {
			a.LoadProjectQueue(q, p)
		}).Disabled(q.Loading || a.access.Busy)
	}
	failed := q.InboxFailed > 0 || q.LastError != ""
	healthy := !setupOnly && q.Loaded && !failed && q.InboxPending == 0 && max(q.PendingDocumentsCount, len(q.Documents)) == 0 &&
		q.DocumentError == "" && q.AutoCaptureError == "" && !p.PolicySyncUnconfirmed()
	var actions func()
	if !setupOnly && !healthy {
		actions = controls
	}
	section := kitui.Group(c.Key("project-queue-section-"+p.ID), tok, "消息与采集状态", "", actions, func() {
		if setupOnly {
			kitui.Row(c, tok, kitui.RowOptions{Key: "proj-queue-setup-" + p.ID, Title: "待配置记忆提炼",
				Desc:    "完成记忆提炼配置后，将自动整理该项目的会话记忆。",
				Control: func() { kitui.SmallButton(c, tok, "立即配置", nil, a.openSettingsFromDeferredBanner) },
			})
			return
		}
		if healthy {
			title, tone := "自动采集已关闭", "neutral"
			if enabled {
				title, tone = "自动采集已开启 · 正常", "ok"
			}
			kitui.Row(c, tok, kitui.RowOptions{Key: "proj-msg-healthy-" + p.ID, Title: title, Leading: func() { kitui.StatusDot(c, tok, tone) }, Control: controls})
			return
		}
		if p.PolicySyncUnconfirmed() {
			kitui.Row(c, tok, kitui.RowOptions{Key: "proj-policy-sync-" + p.ID, Title: "项目权限同步待确认",
				Leading: func() { kitui.StatusDot(c, tok, "warn") },
				Control: func() {
					kitui.SmallButton(c, tok, "重新同步", nil, func() { a.reconcileProjectPolicy(q, p) }).Disabled(a.access.Busy)
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
			kitui.Row(c, tok, kitui.RowOptions{Key: "proj-inbox-failure-" + p.ID, Title: title, Desc: reason, Error: q.InboxRetryError,
				Leading: func() { kitui.StatusDot(c, tok, "fail") },
				Control: func() {
					kitui.SmallPrimaryButton(c, tok, "一键重试", func() { a.RetryProjectInbox(q, p) }).
						Disabled(q.InboxRetryBusy || a.access.Busy || a.config.ProjectControl == nil || !a.canDispatch())
				},
			})
		}
		if q.InboxPending > 0 {
			kitui.Row(c, tok, kitui.RowOptions{Key: "proj-inbox-pending-" + p.ID, Title: fmt.Sprintf("有 %d 条原始记录等待提炼", q.InboxPending),
				Leading: func() { kitui.StatusDot(c, tok, "run") },
			})
		}
		if count := max(q.PendingDocumentsCount, len(q.Documents)); q.Loaded && q.DocumentError == "" && count > 0 {
			kitui.Row(c, tok, kitui.RowOptions{Key: "proj-doc-pending-" + p.ID, Title: fmt.Sprintf("有 %d 份文档等待提炼", count),
				Leading: func() { kitui.StatusDot(c, tok, "run") },
			})
		}
		for _, message := range []string{q.DocumentError, q.AutoCaptureError} {
			if message != "" {
				kitui.Text(c, tok, message, kitui.RoleMeta).TextColor(tok.Danger)
			}
		}
		if !q.Loaded && !failed && q.InboxPending == 0 {
			kitui.SkeletonRow(c, tok, 1, false, false)
		}
	})
	if a.access.RevealQueueProject == p.ID {
		section.ScrollIntoView()
		a.access.RevealQueueProject = ""
	}
}
