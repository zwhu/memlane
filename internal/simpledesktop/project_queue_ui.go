package simpledesktop

import (
	"fmt"
	"github.com/egoist/mygo/ui"
	"time"
)

func (a *App) projectPermissionCard(c *ui.Context, p AccessProject, agent AccessAgent) {
	key := p.ID + "/" + agent.ID
	pending := a.access.CheckboxPending[key]
	ui.Column(c).FillWidth().Gap(16).Padding(20, 22).Background(ui.Hex("#ffffff")).Border(1, homeBorder).Radius(10).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			homeText(c, agent.Name, homeHeading).FontSize(15)
			ui.Spacer(c)
			switch {
			case pending != nil && a.access.Busy:
				homeText(c, "保存中…", homeMeta)
			case pending != nil:
				homeText(c, "尚未确认保存", homeMeta)
				projectLink(c, "重新核对", a.RefreshProjectDirectory).Disabled(a.access.Loading).Tooltip(a.access.Message)
			case time.Now().Before(a.access.PermissionSaved[key]):
				homeText(c, "已保存", homeMeta)
			}
		})
		a.agentPolicyEditor(c, p, agent)
	})
}
func queueBadge(c *ui.Context, label string, color ui.Color) {
	ui.Column(c).Padding(3, 8).Background(color.Alpha(.09)).Radius(4).Children(func() { homeText(c, label, homeMeta).TextColor(color).FontSize(10) })
}
func (a *App) projectQueueSection(c *ui.Context, p AccessProject) {
	q := a.projectQueue(p)
	ui.Column(c).FillWidth().Gap(14).Margin(8, 0, 0, 0).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Children(func() {
			homeText(c, "待处理文档", homeHeading).FontSize(15)
			ui.Spacer(c)
			if q.Loading {
				homeText(c, "读取中…", homeMeta)
			} else {
				readingAction(c, "刷新待处理文档", homeRefresh, func() { a.loadProjectQueue(q, p) })
			}
		})
		ui.Column(c).FillWidth().Border(1, homeBorder).Radius(10).Background(ui.Hex("#ffffff")).Children(func() {
			ui.Row(c).Padding(16, 20).AlignItems(ui.Center).Gap(10).Children(func() {
				homeText(c, "未保存", homeHeading).FontSize(13)
				count, known := 0, q.Loaded
				for _, row := range q.Sessions {
					if !row.View.Loaded {
						known = false
					}
					count += row.View.Status.Pending
				}
				if known {
					homeText(c, fmt.Sprintf("%d 条记录", count), homeMeta)
				}
				ui.Spacer(c)
				homeText(c, "会话记录尚未确认保存", homeMeta)
			})
			shown := 0
			unknown := false
			for n, row := range q.Sessions {
				session := row
				view := row.View
				if !view.Loaded {
					unknown = true
					ui.Row(c).Padding(12, 20).Gap(12).AlignItems(ui.Center).Children(func() {
						homeText(c, session.Agent.Name+" · 会话记录", homeBody).FontSize(12)
						ui.Spacer(c)
						homeText(c, "状态待核对", homeMeta)
						projectLink(c, "重新读取", func() { a.loadProjectQueue(q, p) })
					})
					continue
				}
				if view.Status.Pending == 0 {
					continue
				}
				shown++
				ui.Row(c).Padding(14, 20).BorderWidth(1, 0, 0, 0).BorderColor(homeBorder).Gap(12).AlignItems(ui.Center).Children(func() {
					ui.Icon(c, homeFolder).FontSize(18).TextColor(homeMuted)
					ui.Column(c).Grow(1).MinWidth(0).Gap(5).Children(func() {
						homeText(c, fmt.Sprintf("%s · 会话记录 %d", session.Agent.Name, n+1), homeBody).FontSize(12).Tooltip(session.Binding.HostSessionKey)
						detail := fmt.Sprintf("%d 条记录尚未保存", view.Status.Pending)
						if view.Status.Unknown > 0 {
							detail = fmt.Sprintf("%d 条记录 · %d 条保存结果待确认", view.Status.Pending, view.Status.Unknown)
						}
						homeText(c, detail, homeMeta)
					})
					label := "未保存"
					if view.Status.Unknown > 0 {
						label = "保存结果待核对"
					}
					queueBadge(c, label, ui.Hex("#a47648"))
					tooltip := "重新保存需要此 Agent 的写入权限"
					action := "重新保存"
					if view.Status.Unknown > 0 {
						action = "查看状态"
						tooltip = "重新读取该会话的保存状态，不会重新发送记录"
					}
					if view.Busy {
						action = "处理中…"
					}
					projectLink(c, action, func() {
						mode := "replay"
						if view.Status.Unknown > 0 {
							mode = "show"
						}
						a.runPendingAction(view, p, session.Agent, session.Binding, mode)
						q.Next = time.Now().Add(time.Second)
					}).Disabled(q.Loading || view.Busy || a.access.Busy || (view.Status.Unknown == 0 && p.Overrides[session.Agent.ID].effective(a.access.Directory.Defaults).Access != "maintain")).Tooltip(tooltip)
				})
			}
			if shown == 0 && !unknown {
				label := "当前没有未保存的记录"
				if !q.Loaded {
					label = "正在检查会话保存状态…"
				}
				homeText(c, label, homeMeta).Margin(20)
			}
		})
		ui.Column(c).FillWidth().Border(1, homeBorder).Radius(10).Background(ui.Hex("#ffffff")).Children(func() {
			ui.Row(c).Padding(16, 20).AlignItems(ui.Center).Gap(10).Children(func() {
				homeText(c, "待整理", homeHeading).FontSize(13)
				if q.Loaded && q.DocumentError == "" {
					homeText(c, fmt.Sprintf("%d 份文档", len(q.Documents)), homeMeta)
				}
				ui.Spacer(c)
				homeText(c, "已保存，尚未完成知识整理", homeMeta)
			})
			if q.DocumentError != "" {
				ui.Row(c).Padding(14, 20).Gap(12).Children(func() {
					homeText(c, q.DocumentError, homeMeta)
					projectLink(c, "重新读取", func() { a.loadProjectQueue(q, p) })
				})
			}
			start := min(q.Offset, len(q.Documents))
			end := min(start+5, len(q.Documents))
			for _, row := range q.Documents[start:end] {
				item := row
				ui.Row(c).Padding(14, 20).BorderWidth(1, 0, 0, 0).BorderColor(homeBorder).Gap(12).AlignItems(ui.Center).Children(func() {
					ui.Icon(c, homeDocument).FontSize(18).TextColor(homeMuted)
					ui.Column(c).Grow(1).MinWidth(0).Gap(5).Children(func() {
						homeText(c, item.Title, homeBody).FontSize(12).SingleLine().Tooltip(item.Title)
						if item.Date != "" {
							homeText(c, displayDate(item.Date), homeMeta)
						}
					})
					queueBadge(c, "未完成整理", homeLinkInk)
					projectLink(c, "查看", func() { a.openQueuedDocument(p, item) })
				})
			}
			if len(q.Documents) == 0 && q.DocumentError == "" {
				label := "当前没有待整理的文档"
				if !q.Loaded {
					label = "正在读取文档整理状态…"
				}
				homeText(c, label, homeMeta).Margin(20)
			}
			if len(q.Documents) > 5 {
				ui.Row(c).Padding(12, 20).Gap(12).Children(func() {
					ui.Spacer(c)
					projectLink(c, "上一页", func() { q.Offset = max(0, q.Offset-5) }).Disabled(start == 0)
					homeText(c, fmt.Sprintf("%d–%d / %d", start+1, end, len(q.Documents)), homeMeta)
					projectLink(c, "下一页", func() { q.Offset += 5 }).Disabled(end == len(q.Documents))
				})
			}
		})
	})
}
func (a *App) openQueuedDocument(p AccessProject, item Item) {
	for index, project := range a.Config.Projects {
		if project.ID == p.ID {
			origin := a.settingsOrigin
			a.navigateKind("知识", index, "文档")
			a.projectUI.QueueBack = origin
			a.projectUI.QueueProject = p.ID
			a.selectItem(a.state(), item)
			return
		}
	}
}
