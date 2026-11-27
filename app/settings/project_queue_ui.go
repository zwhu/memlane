package settings

import (
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	kitui "github.com/zwhu/memlane/kit/ui"
)

func (a *Controller) projectPermissionCard(c *ui.Context, p AccessProject, agent AccessAgent) {
	tok := a.tokens()
	key := p.ID + "/" + agent.ID
	actual := p.EffectivePolicy(a.access.Directory.Defaults, agent)
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
	filteredCount, filtered := q.FilteredCount, q.RecentFiltered
	if !q.inboxLoaded && filteredCount == 0 && len(filtered) == 0 && q.DocumentError == "" {
		filteredCount, filtered = p.FilteredCount, p.RecentFiltered
	}
	section := kitui.Group(c.Key("project-queue-section-"+p.ID), tok, "消息与采集状态", "", nil, func() {
		if setupOnly {
			kitui.Row(c, tok, kitui.RowOptions{Key: "proj-queue-setup-" + p.ID, Title: "待配置记忆提炼",
				Desc:    "完成记忆提炼配置后，将自动整理该项目的会话记忆。",
				Control: func() { kitui.SmallButton(c, tok, "立即配置", nil, a.openSettingsFromDeferredBanner) },
			})
			return
		}
		title, desc, tone := q.autoCaptureStatus(p)
		kitui.Row(c, tok, kitui.RowOptions{Key: "proj-capture-status-" + p.ID, Title: title, Desc: desc,
			Leading: func() { kitui.StatusDot(c, tok, tone) }, Control: controls})
		if filteredCount > 0 || len(filtered) > 0 {
			kitui.Row(c, tok, kitui.RowOptions{Key: "proj-filtered-" + p.ID, Title: "因采集规则未采集", Desc: filteredCaptureDescription(filtered),
				Leading: func() { kitui.StatusDot(c, tok, "warn") },
				Control: func() {
					if len(filtered) > 1 {
						label := "查看近期记录"
						if q.ShowFilteredDetails {
							label = "收起近期记录"
						}
						kitui.SmallButton(c, tok, label, nil, func() { q.ShowFilteredDetails = !q.ShowFilteredDetails })
					}
				},
			})
			if q.ShowFilteredDetails {
				for _, event := range filtered {
					kitui.Row(c, tok, kitui.RowOptions{Key: "proj-filter-event-" + event.ID,
						Title: formatEventTimeLocal(event.OccurredAt), Desc: event.Reason,
						Control: func() { kitui.Badge(c, tok, event.Rule, "warn") }})
				}
			}
		}
		if healthy {
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
			title := "最近采集失败"
			if q.InboxFailed > 0 {
				title = fmt.Sprintf("采集队列有 %d 项失败", q.InboxFailed)
			}
			headline, detail, tech := formatInboxFailureUI(q.LastError, q.LastErrorAt, q.RecentFailures)
			ui.Column(c.Key("proj-inbox-failure-item-" + p.ID)).FillWidth().Gap(0).Children(func() {
				kitui.Row(c, tok, kitui.RowOptions{Key: "proj-inbox-failure-" + p.ID, Title: title, Desc: headline + " · " + detail, Error: q.InboxRetryError,
					Leading: func() { kitui.StatusDot(c, tok, "fail") },
					Control: func() {
						if tech != "" {
							label := "查看技术详情"
							if q.ShowFailureTech {
								label = "收起技术详情"
							}
							kitui.SmallButton(c, tok, label, nil, func() { q.ShowFailureTech = !q.ShowFailureTech })
						}
						kitui.SmallPrimaryButton(c, tok, "一键重试", func() { a.RetryProjectInbox(q, p) }).
							Disabled(q.InboxRetryBusy || a.access.Busy || a.config.ProjectControl == nil || !a.canDispatch())
					},
				})
				if q.ShowFailureTech && tech != "" {
					ui.Column(c.Key("proj-inbox-tech-" + p.ID)).FillWidth().Padding(0, sp4, sp3, sp4+18).Children(func() {
						ui.Column(c).FillWidth().Padding(sp2, sp3).Gap(sp1).Background(tok.Bg).Border(1, tok.Border).Radius(radius).Children(func() {
							for _, line := range strings.Split(tech, "\n") {
								if line != "" {
									kitui.Text(c, tok, line, kitui.RoleMeta)
								}
							}
						})
					})
				}
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
