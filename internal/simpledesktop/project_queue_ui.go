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
		ui.Row(c).Width(180).MinWidth(0).AlignItems(ui.Center).Gap(8).Children(func() {
			agentLogo(c, accessAgentIcon(agent), 16)
			homeText(c, agentDisplayName(agent.Name), homeBody).SingleLine()
		})
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
	ui.Row(c).Height(24).Shrink(0).Justify(ui.Start).AlignItems(ui.Center).Children(func() { queueBadge(c, label, color) })
}
func queueAction(c *ui.Context, label string, fn func()) ui.Element {
	return textButton(c, label, fn).Shrink(0).Padding(0).AlignSelf(ui.Center).Justify(ui.Start).TextColor(tok.Accent)
}
func queueRefreshAction(c *ui.Context, label string, fn func()) ui.Element {
	return refreshAction(c, label, fn)
}

func (a *App) projectQueueSection(c *ui.Context, p AccessProject) {
	q := a.projectQueue(p)
	needs, info := a.messageNotices(q, p)
	var history []messageNotice
	active := func(rows []messageNotice) []messageNotice {
		var result []messageNotice
		for _, n := range rows {
			if q.noticeAcknowledged(n.Key) {
				history = append(history, n)
			} else {
				result = append(result, n)
			}
		}
		return result
	}
	needs, info = active(needs), active(info)
	ui.Column(c.Key("message-status-"+p.ID)).FillWidth().Gap(16).Margin(8, 0, 0, 0).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(12).Children(func() {
			homeText(c, "消息与处理状态", homeHeading).FontSize(15)
			if q.Loading {
				homeText(c, "更新中…", homeMeta)
			}
			if q.AutomaticSave {
				homeText(c, "自动保存已开启", homeMeta)
			}
			if q.Backlog != nil && (q.AutomaticSave || q.Backlog.active()) {
				queueAction(c, "暂停自动保存", func() { a.changeBacklog(q, p, true) }).Disabled(q.BacklogBusy || q.Backlog.Status == "stopping")
			}
			refreshAction(c, "刷新消息状态", func() { a.loadProjectQueue(q, p); a.refreshBacklog(q, p) }).Disabled(q.Loading || q.BacklogLoading || a.access.Busy)
		})
		a.messageNoticeGroup(c, q, p, "需要你处理", "仅列出需要你操作的事项。", needs, true)
		a.messageNoticeGroup(c, q, p, "状态与异常提示", "无需你确认消息是否保存；系统异常会保留显示。", info, false)
		if len(history) > 0 {
			projectLink(c, fmt.Sprintf("已知晓的历史提示（%d）", len(history)), func() { q.NoticeHistoryOpen = !q.NoticeHistoryOpen })
			if q.NoticeHistoryOpen {
				a.messageNoticeGroup(c, q, p, "已知晓的历史提示", "", history, false)
			}
		}
		if q.NoticeAcknowledgementError != "" {
			homeText(c, q.NoticeAcknowledgementError, homeBody).TextColor(tok.Warning)
		}
		if !q.Loaded && len(info) == 0 {
			homeText(c, "正在读取消息状态…", homeMeta)
		}
		if q.Loaded && len(q.Documents) > 0 && q.DocumentError == "" {
			ui.Column(c).FillWidth().Gap(10).Children(func() {
				ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
					homeText(c, "记忆整理", homeHeading).FontSize(13)
					queueBadge(c, fmt.Sprintf("%d 份文档", len(q.Documents)), homeLinkInk)
				})
				homeText(c, "以下文档已保存，尚未完成记忆整理。", homeMeta)
				start := min(q.Offset, len(q.Documents))
				end := min(start+5, len(q.Documents))
				for _, item := range q.Documents[start:end] {
					item := item
					ui.Column(c.Key("document-"+item.ID)).FillWidth().Padding(14, 16).Gap(6).Background(tok.BgElevated).Border(1, homeBorder).Shadow(0, 2, 8, 0, panelShadowInk).Radius(10).Children(func() {
						homeText(c, queuedDocumentTitle(item), homeBody).FontSize(12).SingleLine()
						ui.Row(c).AlignItems(ui.Center).Gap(12).Children(func() {
							queueBadge(c, "已保存 · 未完成整理", homeLinkInk)
							if item.Date != "" {
								homeText(c, displayDate(item.Date), homeMeta)
							}
							queueAction(c, "查看", func() { a.openQueuedDocument(p, item) })
						})
					})
				}
				if len(q.Documents) > 5 {
					ui.Row(c).Gap(12).Children(func() {
						queueAction(c, "上一页", func() { q.Offset = max(0, q.Offset-5) }).Disabled(start == 0)
						homeText(c, fmt.Sprintf("%d–%d / %d", start+1, end, len(q.Documents)), homeMeta)
						queueAction(c, "下一页", func() { q.Offset += 5 }).Disabled(end == len(q.Documents))
					})
				}
			})
		}
		if q.ArchiveError != "" {
			homeText(c, "历史异常记录读取失败；当前保存异常仍会显示。", homeMeta)
		}
	})
}

var messageAlertIcon = homeSVG(`<circle cx="12" cy="12" r="9"/><path d="M12 7v6m0 4h.01"/>`)
var messageClockIcon = homeSVG(`<circle cx="12" cy="12" r="9"/><path d="M12 7v5l3 2"/>`)
var messagePauseIcon = homeSVG(`<rect x="7" y="5" width="3" height="14" rx="1"/><rect x="14" y="5" width="3" height="14" rx="1"/>`)

func (a *App) messageNoticeGroup(c *ui.Context, q *projectQueueState, p AccessProject, title, subtitle string, rows []messageNotice, needsUser bool) {
	ui.Column(c.Key(title)).FillWidth().Gap(9).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			homeText(c, title, homeHeading).FontSize(12)
			if len(rows) > 0 {
				homeText(c, fmt.Sprintf("%d", len(rows)), homeMeta).FontSize(11)
			}
			if len(rows) == 0 && q.Loaded {
				label := "当前没有需要你处理的事项"
				if !needsUser {
					label = "当前没有消息积压或已检测到的异常"
				}
				homeText(c, label, homeMeta)
			}
		})
		if len(rows) == 0 {
			return
		}
		ui.Column(c).FillWidth().Background(tok.BgElevated).Border(1, homeBorder).Shadow(0, 2, 8, 0, panelShadowInk).Radius(10).Children(func() {
			for index, n := range rows {
				n := n
				acknowledged := q.noticeAcknowledged(n.Key)
				ui.Row(c.Key(n.Key)).FillWidth().Padding(13, 16).Gap(12).AlignItems(ui.Start).Children(func() {
					color := homeIconInk
					icon := messageClockIcon
					switch n.Tag {
					case "系统异常", "读取异常", "整理异常", "保存受阻", "需要授权", "需要补交":
						color = tok.Warning
						icon = messageAlertIcon
					case "已暂停":
						icon = messagePauseIcon
					}
					ui.Column(c).Width(26).Height(26).AlignItems(ui.Center).Justify(ui.Center).Background(color.Alpha(.07)).Radius(7).Children(func() { ui.Icon(c, icon).FontSize(15).TextColor(color) })
					ui.Column(c).Grow(1).MinWidth(0).Gap(5).Children(func() {
						ui.Row(c).AlignItems(ui.Center).Gap(9).Children(func() {
							homeText(c, n.Title, homeBody).FontSize(12).FontWeight(550).TextColor(homeInk)
							queueBadge(c, n.Tag, color)
							if acknowledged {
								label := "已知晓 · 不再提醒"
								if q.NoticeAcknowledgements[noticeHash(n.Key)].Deferred {
									label = "暂不处理 · 不再提醒"
								}
								homeText(c, label, homeMeta)
							}
						})
						if n.Source != "" {
							ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
								if n.Session != nil {
									agentLogo(c, accessAgentIcon(n.Session.Agent), 12)
								}
								homeText(c, n.Source, homeMeta).FontSize(10).TextColor(tok.Text3)
							})
						}
						homeText(c, n.Detail, homeBody).FontSize(11).TextColor(tok.Text2).LineHeight(1.45)
						if n.Followup != "" {
							homeText(c, n.Followup, homeMeta).LineHeight(1.45)
						}
						if n.Session != nil && n.Session.View.Message != "" && n.Session.View.ReadError == "" {
							homeText(c, n.Session.View.Message, homeMeta).LineHeight(1.45)
						}
						ui.Row(c).AlignItems(ui.Center).Gap(12).Children(func() {
							switch n.Action {
							case "permissions":
								queueAction(c, "查看权限设置", func() { q.Scroll.Y = 0 })
							case "replay":
								if n.Session != nil {
									row := *n.Session
									queueAction(c, "补交这些消息", func() { a.runPendingAction(row.View, p, row.Agent, row.Binding, "replay") }).Disabled(row.View.Busy || a.access.Busy || row.View.Status.Unknown > 0 || row.View.Status.Signature == "" || row.Agent.Installation == nil)
								}
							case "resume":
								queueAction(c, "恢复保存", func() { a.changeBacklog(q, p, false) }).Disabled(q.BacklogBusy || a.access.Busy || a.Config.ProjectControl == nil)
							case "save":
								queueAction(c, "开始保存", func() { a.changeBacklog(q, p, false) }).Disabled(q.BacklogBusy || q.Loading || a.access.Busy || a.Config.ProjectControl == nil)
							case "refresh":
								queueRefreshAction(c, "重新读取状态", func() { a.loadProjectQueue(q, p); a.refreshBacklog(q, p) }).Disabled(q.Loading || a.access.Busy)
							}
							if n.Session != nil {
								v := n.Session.View
								label := "查看详情"
								if v.CheckDetailsOpen {
									label = "收起详情"
								}
								queueAction(c, label, func() { v.CheckDetailsOpen = !v.CheckDetailsOpen })
							}
							if acknowledged {
								queueAction(c, "恢复提醒", func() { a.acknowledgeNotice(q, p, n, true) })
							} else {
								label := "知道了，不再提醒"
								if n.NeedsUser {
									label = "暂不处理"
								}
								queueAction(c, label, func() { a.acknowledgeNotice(q, p, n, false) }).Tooltip("仅结束这项提醒，保留原消息和异常记录；出现新的异常会再次提醒。")
							}
						})
						if n.Session != nil && n.Session.View.CheckDetailsOpen {
							a.messageNoticeDetails(c, q, p, *n.Session)
						}
					})
				})
				if index < len(rows)-1 {
					ui.Column(c).FillWidth().Padding(0, 16).Children(func() { ui.Column(c).Height(1).FillWidth().Background(homeBorder.Alpha(.7)) })
				}
			}
		})
	})
}
func (a *App) messageNoticeDetails(c *ui.Context, q *projectQueueState, p AccessProject, row queuedSession) {
	v := row.View
	check := q.pendingCheck(row)
	ui.Column(c).FillWidth().Margin(7, 0, 0, 0).Padding(14, 16).Gap(12).Background(tok.BgElevated).Border(1, homeBorder).Shadow(0, 2, 8, 0, panelShadowInk).Radius(8).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(12).Children(func() {
			homeText(c, "保存详情", homeHeading).FontSize(12).TextColor(homeInk)
			label := "诊断信息"
			if v.DiagnosticsOpen {
				label = "收起诊断信息"
			}
			queueAction(c, label, func() { v.DiagnosticsOpen = !v.DiagnosticsOpen })
			ui.Spacer(c)
			if check.CheckedAt > 0 {
				homeText(c, "检查于 "+time.Unix(int64(check.CheckedAt), 0).Format("01月02日 15:04"), homeMeta).TextColor(tok.Text3)
			}
		})
		homeText(c, "会话："+shortSessionLabel(row.Binding.HostSessionKey), homeMeta).TextColor(tok.Text3).Tooltip(row.Binding.HostSessionKey)
		if v.DiagnosticsOpen {
			ui.Column(c).FillWidth().Padding(10, 12).Gap(6).Background(tok.Fill1).Radius(6).Children(func() {
				homeText(c, "会话："+row.Binding.HostSessionKey, homeMeta)
				if v.ReadCode != "" {
					homeText(c, "诊断代码："+v.ReadCode, homeMeta)
				}
				if check.Reason != "" && check.Reason != v.ReadCode {
					homeText(c, "诊断代码："+check.Reason, homeMeta)
				}
				if len(v.Status.UnknownEntryIDs) > 0 {
					homeText(c, fmt.Sprintf("%d 条消息有本机标识；标识不代表服务端已保存。", len(v.Status.UnknownEntryIDs)), homeMeta)
				}
			})
		}
		if check.Status == "unconfirmed" && check.Reason != "original_receipt_unavailable" {
			ui.Row(c).AlignItems(ui.Center).Children(func() {
				queueAction(c, "重新检查保存结果", func() { a.retryPendingCheck(q, p, row) }).Disabled(v.Busy || a.access.Busy)
			})
		}
		if v.Status.Blocked != nil {
			homeText(c, "暂停原因："+backlogReason(v.Status.Blocked.Reason), homeBody).FontSize(11).TextColor(homeInk)
		}
		if v.Loaded && v.Status.Pending > 0 {
			if row.Agent.Installation != nil && row.Agent.Installation.HostKind != "opencode" {
				ui.Row(c).AlignItems(ui.Center).Children(func() {
					queueAction(c, "查看本机消息", func() { a.runPendingAction(v, p, row.Agent, row.Binding, "details") }).Disabled(v.Busy || a.access.Busy)
				})
			} else {
				homeText(c, "当前接入暂不支持读取本机消息摘要。", homeMeta)
			}
		}
		if v.DetailsError != "" {
			homeText(c, v.DetailsError, homeBody).FontSize(11).TextColor(tok.Warning)
		}
		if v.DetailsLoaded {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
				homeText(c, "本机消息", homeHeading).FontSize(12).TextColor(homeInk)
				homeText(c, fmt.Sprintf("已读取 %d 条 · 最多 20 条", len(v.Details)), homeMeta).TextColor(tok.Text3)
			})
			visible := len(v.Details)
			if !v.AllMessagesOpen {
				visible = min(5, visible)
			}
			for index, entry := range v.Details[:visible] {
				entry := entry
				key := fmt.Sprintf("%s/%d", entry.ID, index)
				ui.Column(c.Key(key)).FillWidth().Gap(7).Padding(10, 12).Background(tok.Fill1).Radius(6).Children(func() {
					ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
						homeText(c, fmt.Sprintf("%02d", index+1), homeMeta).TextColor(tok.Text3).Width(20)
						homeText(c, entry.Kind, homeBody).FontSize(11).FontWeight(550).TextColor(homeInk)
						ui.Spacer(c)
						if entry.Unknown {
							queueBadge(c, "结果未确认", tok.Warning)
						} else {
							queueBadge(c, "待保存", tok.Text2)
						}
						label := "展开"
						if v.ExpandedMessages[key] {
							label = "收起"
						}
						queueAction(c, label, func() {
							if v.ExpandedMessages == nil {
								v.ExpandedMessages = map[string]bool{}
							}
							v.ExpandedMessages[key] = !v.ExpandedMessages[key]
						})
					})
					if v.ExpandedMessages[key] {
						homeText(c, entry.Summary, homeBody).FontSize(12).TextColor(homeInk).LineHeight(1.55)
					} else {
						homeText(c, entry.Summary, homeBody).FontSize(12).TextColor(homeInk).SingleLine().Tooltip(entry.Summary)
					}
					if v.DiagnosticsOpen {
						homeText(c, "消息标识："+entry.ID, homeMeta).SingleLine().Tooltip(entry.ID)
					}
				})
			}
			if len(v.Details) > 5 {
				label := fmt.Sprintf("显示其余 %d 条", len(v.Details)-5)
				if v.AllMessagesOpen {
					label = "收起为 5 条"
				}
				queueAction(c, label, func() { v.AllMessagesOpen = !v.AllMessagesOpen })
			}
			if len(v.Details) == 0 {
				homeText(c, "当前未读取到消息摘要，请刷新状态。", homeMeta)
			}
		}

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
