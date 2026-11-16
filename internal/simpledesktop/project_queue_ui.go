package simpledesktop

import (
	"fmt"
	"github.com/egoist/mygo/ui"
	"time"
)

func (a *App) projectPermissionCard(c *ui.Context, p AccessProject, agent AccessAgent) {
	key := p.ID + "/" + agent.ID
	pending := a.access.CheckboxPending[key]
	ui.Row(c.Key("permission-row-"+key)).FillWidth().AlignItems(ui.Center).Gap(16).Padding(12, 16).Children(func() {
		homeText(c, agentDisplayName(agent.Name), homeBody).Width(180).MinWidth(0)
		a.agentPolicyEditor(c, p, agent)
		ui.Spacer(c)
		ui.Row(c).Width(120).AlignItems(ui.Center).Justify(ui.End).Gap(8).Children(func() {
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
	})
}
func queueBadge(c *ui.Context, label string, color ui.Color) {
	ui.Row(c).Height(20).Padding(0, 8).AlignItems(ui.Center).Background(color.Alpha(.09)).Radius(4).Children(func() { homeText(c, label, homeMeta).TextColor(color).FontSize(10) })
}
func queueStatus(c *ui.Context, label string, color ui.Color) {
	ui.Row(c).Width(112).Height(24).Shrink(0).Justify(ui.End).AlignItems(ui.Center).Children(func() { queueBadge(c, label, color) })
}
func queueAction(c *ui.Context, label string, fn func()) ui.Element {
	return textButton(c, label, fn).Width(112).MinWidth(112).Shrink(0).Padding(0).AlignSelf(ui.Center).Justify(ui.End)
}
func queueRefreshAction(c *ui.Context, label string, fn func()) ui.Element {
	var action ui.Element
	ui.Row(c).Width(112).Shrink(0).AlignSelf(ui.Center).Justify(ui.End).Children(func() {
		action = refreshAction(c, label, fn)
	})
	return action
}
func (a *App) projectQueueSection(c *ui.Context, p AccessProject) {
	q := a.projectQueue(p)
	ui.Column(c).FillWidth().Gap(14).Margin(8, 0, 0, 0).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Children(func() {
			homeText(c, "待处理内容", homeHeading).FontSize(15)
			ui.Spacer(c)
			if q.Loading {
				homeText(c, "读取中…", homeMeta)
			} else {
				refreshAction(c, "刷新待处理文档", func() { a.loadProjectQueue(q, p) })
			}
		})
		ui.Column(c).FillWidth().Border(1, homeBorder).Radius(10).Background(ui.Hex("#ffffff")).Children(func() {
			ui.Column(c).Padding(16, 20).Gap(6).Children(func() {
				homeText(c, "会话保存", homeHeading).FontSize(13)
				pending, uncertain, unread := 0, 0, 0
				for _, row := range q.Sessions {
					if !row.View.Loaded {
						unread++
						continue
					}
					status := q.activePending(row)
					pending += max(0, status.Pending-status.Unknown)
					uncertain += status.Unknown
				}
				if q.Loaded {
					summary := fmt.Sprintf("%d 条待保存 · %d 条保存状态待确认", pending, uncertain)
					if unread > 0 {
						summary += fmt.Sprintf(" · %d 个会话暂未读取", unread)
					}
					homeText(c, summary, homeMeta)
				} else {
					homeText(c, "正在读取会话保存状态…", homeMeta)
				}
			})
			a.backlogControls(c, q, p)
			shown := 0
			unknown := false
			for _, row := range q.Sessions {
				session := row
				view := row.View
				status := q.activePending(row)
				if !view.Loaded {
					unknown = true
					ui.Row(c).Padding(12, 20).Gap(12).AlignItems(ui.Center).Children(func() {
						homeText(c, agentDisplayName(session.Agent.Name)+" · 会话状态", homeBody).FontSize(12).Grow(1).MinWidth(0)
						queueStatus(c, "暂未读取", homeMuted)
						label := "重新读取"
						if q.Loading {
							label = "读取中…"
						}
						queueRefreshAction(c, label, func() { a.loadProjectQueue(q, p) }).Disabled(q.Loading || a.access.Busy)
					})
					if view.Message != "" {
						homeText(c, "暂时无法读取保存状态，请重新读取。", homeMeta).Tooltip(view.Message).Margin(0, 20, 12, 20)
					}
					continue
				}
				if status.Pending == 0 && !view.Busy {
					continue
				}
				shown++
				ui.Row(c).Padding(14, 20).BorderWidth(1, 0, 0, 0).BorderColor(homeBorder).Gap(12).AlignItems(ui.Center).Children(func() {
					ui.Icon(c, homeDocument).FontSize(18).TextColor(homeMuted)
					ui.Column(c).Grow(1).MinWidth(0).Gap(5).Children(func() {
						homeText(c, agentDisplayName(session.Agent.Name)+" · 会话 "+shortSessionLabel(session.Binding.HostSessionKey), homeBody).FontSize(12).Tooltip(session.Binding.HostSessionKey)
						detail := fmt.Sprintf("%d 条待保存", status.Pending)
						if status.Unknown > 0 {
							detail = fmt.Sprintf("%d 条记录保存状态待确认", status.Unknown)
							if remaining := status.Pending - status.Unknown; remaining > 0 {
								detail += fmt.Sprintf(" · 另有 %d 条待保存", remaining)
							}
							if q.Backlog.active() && !q.Backlog.Automatic {
								detail = fmt.Sprintf("%d 条待保存 · %d 条正在确认保存结果", status.Pending-status.Unknown, status.Unknown)
							}
						}
						homeText(c, detail, homeMeta)
						if view.Message != "" {
							homeText(c, view.Message, homeMeta).TextColor(homeLinkInk)
						}
						if status.Unknown > 0 {
							check := q.pendingCheck(session)
							message := "后台会自动检查，原记录继续保留。"
							if check.Status == "checking" {
								message = "正在查询保存结果，不会重复发送。"
							}
							if check.Status == "unconfirmed" {
								message = "保存结果无法确认；可归档结束提醒，原记录继续保留。"
							}
							homeText(c, message, homeMeta)
							projectLink(c, "检查详情", func() { view.CheckDetailsOpen = !view.CheckDetailsOpen })
							if view.CheckDetailsOpen {
								detail := "尚未收到后台检查结果。"
								if check.CheckedAt > 0 {
									detail = "上次检查：" + time.Unix(int64(check.CheckedAt), 0).Format("01月02日 15:04")
								}
								homeText(c, detail, homeMeta)
								if check.Reason == "original_receipt_unavailable" {
									homeText(c, "服务端未提供可唯一确认原写入的回执；内容相似或最近记录中未找到，均不能确定是否保存。", homeMeta).MaxLines(3)
								} else if check.Reason != "" {
									homeText(c, "暂时无法读取服务端保存结果，原记录未更改。", homeMeta)
								}
							}
						}
					})
					label := "未保存"
					if status.Unknown > 0 {
						label = "等待检查"
						check := q.pendingCheck(session)
						if check.Status == "checking" {
							label = "检查中"
						}
						if check.Status == "unconfirmed" {
							label = "暂无法确认"
						}
						if q.Backlog.active() && !q.Backlog.Automatic {
							label = "确认中"
						}
					}
					queueStatus(c, label, ui.Hex("#a47648"))
					if status.Unknown == 0 {
						ui.Spacer(c).Width(112).Grow(0).Shrink(0)
						return
					}
					check := q.pendingCheck(session)
					if check.Status == "unconfirmed" && check.Reason == "original_receipt_unavailable" {
						_, ready := pendingIDCounts(view.Status)
						queueAction(c, "归档此异常", func() { a.archivePending(q, p, session) }).Disabled(!ready || view.Busy || a.access.Busy).Tooltip("结束这批异常的提醒；保留原材料和未知状态，不重新发送")
					} else if check.Status == "unconfirmed" {
						queueAction(c, "重试检查", func() { a.retryPendingCheck(q, p, session) }).Disabled(view.Busy || a.access.Busy).Tooltip("只查询保存结果，不重新发送记录")
					} else {
						ui.Spacer(c).Width(112).Grow(0).Shrink(0)
					}
				})
			}
			if shown == 0 && !unknown {
				label := "当前没有未保存的记录"
				if len(q.Archives) > 0 {
					label = "当前没有待处理的保存事项，已归档异常可在下方查看。"
				}
				if !q.Loaded {
					label = "正在检查会话保存状态…"
				}
				homeText(c, label, homeMeta).Margin(20)
			}
		})
		a.pendingArchiveHistory(c, q)
		ui.Column(c).FillWidth().Border(1, homeBorder).Radius(10).Background(ui.Hex("#ffffff")).Children(func() {
			ui.Row(c).Padding(16, 20).AlignItems(ui.Center).Gap(10).Children(func() {
				homeText(c, "待整理", homeHeading).FontSize(13)
				if q.Loaded && q.DocumentError == "" {
					homeText(c, fmt.Sprintf("%d 份文档", len(q.Documents)), homeMeta)
				}
				ui.Spacer(c)
				homeText(c, "已保存，尚未完成记忆整理", homeMeta)
			})
			if q.DocumentError != "" {
				ui.Row(c).Padding(14, 20).Gap(12).Children(func() {
					homeText(c, q.DocumentError, homeMeta)
					ui.Spacer(c)
					refreshAction(c, "重新读取", func() { a.loadProjectQueue(q, p) }).Disabled(q.Loading || a.access.Busy)
				})
			}
			start := min(q.Offset, len(q.Documents))
			end := min(start+5, len(q.Documents))
			for _, row := range q.Documents[start:end] {
				item := row
				ui.Row(c).Padding(14, 20).BorderWidth(1, 0, 0, 0).BorderColor(homeBorder).Gap(12).AlignItems(ui.Center).Children(func() {
					ui.Icon(c, homeDocument).FontSize(18).TextColor(homeMuted)
					ui.Column(c).Grow(1).MinWidth(0).Gap(5).Children(func() {
						homeText(c, queuedDocumentTitle(item), homeBody).FontSize(12).SingleLine().Tooltip(queuedDocumentTitle(item))
						if item.Date != "" {
							homeText(c, displayDate(item.Date), homeMeta)
						}
					})
					queueStatus(c, "未完成整理", homeLinkInk)
					queueAction(c, "查看", func() { a.openQueuedDocument(p, item) })
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
					queueAction(c, "上一页", func() { q.Offset = max(0, q.Offset-5) }).Disabled(start == 0)
					homeText(c, fmt.Sprintf("%d–%d / %d", start+1, end, len(q.Documents)), homeMeta)
					queueAction(c, "下一页", func() { q.Offset += 5 }).Disabled(end == len(q.Documents))
				})
			}
		})
	})
}
func (a *App) openQueuedDocument(p AccessProject, item Item) {
	q := a.projectQueueStateFor(p)
	reader := &pane{DetailScroll: map[string]*ui.ScrollState{}, Tools: map[string]bool{}, Technical: map[string]bool{}}
	q.Reader = reader
	// Register with the existing reading lifecycle so identity changes and
	// navigation cancel/withdraw this reader just like the ordinary panes.
	if a.panes == nil {
		a.panes = map[string]*pane{}
	}
	a.panes["queue:"+p.ID] = reader
	a.selectItem(reader, item)
}
