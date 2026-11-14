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
			added := 0
			for _, row := range q.Sessions {
				added += row.View.Added
			}
			ui.Row(c).Padding(16, 20).AlignItems(ui.Center).Gap(10).Children(func() {
				homeText(c, "待保存与核对", homeHeading).FontSize(13)
				count, uncertain, known := 0, 0, q.Loaded
				for _, row := range q.Sessions {
					if !row.View.Loaded {
						known = false
					}
					count += row.View.Status.Pending
					uncertain += row.View.Status.Unknown
				}
				if known {
					homeText(c, fmt.Sprintf("%d 条记录", count), homeMeta)
					if uncertain > 0 {
						label := fmt.Sprintf("含 %d 条结果待核对", uncertain)
						if q.Backlog.active() {
							label = fmt.Sprintf("含 %d 条等待回执或核对", uncertain)
						}
						homeText(c, label, homeMeta)
					}
				}
				ui.Spacer(c)
				homeText(c, "按真实会话汇总", homeMeta)
			})
			ui.Column(c).Padding(0, 20, 12, 20).Gap(5).Children(func() {
				homeText(c, "新消息单独计入队列；本次积压任务持续保存已确认的范围。", homeMeta)
				if added > 0 {
					homeText(c, fmt.Sprintf("新增 %d 条待保存记录（包含新会话）", added), homeMeta).TextColor(homeLinkInk)
				}
			})
			a.backlogControls(c, q, p)
			shown := 0
			unknown := false
			for _, row := range q.Sessions {
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
					if view.Message != "" {
						homeText(c, view.Message, homeMeta).Margin(0, 20, 12, 20)
					}
					continue
				}
				if view.Status.Pending == 0 && view.Message == "" && !view.Busy {
					continue
				}
				shown++
				ui.Row(c).Padding(14, 20).BorderWidth(1, 0, 0, 0).BorderColor(homeBorder).Gap(12).AlignItems(ui.Center).Children(func() {
					ui.Icon(c, homeFolder).FontSize(18).TextColor(homeMuted)
					ui.Column(c).Grow(1).MinWidth(0).Gap(5).Children(func() {
						homeText(c, session.Agent.Name+" · 会话 "+shortSessionLabel(session.Binding.HostSessionKey), homeBody).FontSize(12).Tooltip(session.Binding.HostSessionKey)
						detail := fmt.Sprintf("%d 条待保存", view.Status.Pending)
						if view.Status.Unknown > 0 {
							detail = fmt.Sprintf("%d 条待发送 · %d 条保存结果待核对", view.Status.Pending-view.Status.Unknown, view.Status.Unknown)
							if q.Backlog.active() {
								detail = fmt.Sprintf("%d 条待发送 · %d 条等待回执或核对", view.Status.Pending-view.Status.Unknown, view.Status.Unknown)
							}
						}
						homeText(c, detail, homeMeta)
						if view.Message != "" {
							homeText(c, view.Message, homeMeta).TextColor(homeLinkInk)
						}
					})
					label := "未保存"
					if view.Status.Unknown > 0 {
						label = "保存结果待核对"
						if q.Backlog.active() {
							label = "等待回执／核对"
						}
					}
					queueBadge(c, label, ui.Hex("#a47648"))
					if view.Status.Unknown == 0 {
						return
					}
					tooltip := "读取服务端最近记录并比对，不发送、不删除原记录"
					action := "核对保存结果"
					if view.Status.Unknown > 0 {
						action = "核对保存结果"
						tooltip = "读取服务端最近记录并比对，不发送、不删除原记录"
					}
					if view.Busy {
						action = "处理中…"
					}
					projectLink(c, action, func() {
						a.runPendingAction(view, p, session.Agent, session.Binding, "check")
						q.Next = time.Now().Add(time.Second)
					}).Disabled(q.Backlog.active() || q.Loading || view.Busy || a.access.Busy || view.Status.Pending == 0 || view.Status.Signature == "").Tooltip(tooltip)
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
