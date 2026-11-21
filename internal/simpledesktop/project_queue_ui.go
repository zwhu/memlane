package simpledesktop

import (
	"fmt"
	"github.com/egoist/mygo/ui"
	"time"
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
func queueBadge(c *ui.Context, label string, color ui.Color) {
	ui.Row(c).Height(20).Padding(0, 7).AlignItems(ui.Center).Background(color.Alpha(.10)).Radius(radiusSmall).Children(func() {
		homeText(c, label, homeMeta).TextColor(color).FontSize(fsCaption).FontWeight(500)
	})
}
func queueStatus(c *ui.Context, label string, color ui.Color) {
	ui.Row(c).Height(24).Shrink(0).Justify(ui.Start).AlignItems(ui.Center).Children(func() { queueBadge(c, label, color) })
}
func queueAction(c *ui.Context, label string, fn func()) ui.Element {
	return v4SmallButton(c, label, nil, fn)
}
func queueRefreshAction(c *ui.Context, label string, fn func()) ui.Element {
	return v4SmallButton(c, label, v4IconRefresh, fn)
}

func pickProjectQueueAlertTarget(q *projectQueueState, needs, info []messageNotice, now time.Time) (string, bool) {
	alertKeys, ackErr := q.unacknowledgedNotificationKeys(now)
	for _, n := range needs {
		if n.Key != "" && alertKeys[n.Key] {
			return n.Key, false
		}
	}
	for _, n := range info {
		if n.Key != "" && alertKeys[n.Key] {
			return n.Key, false
		}
	}
	if ackErr {
		return "", true
	}
	if len(needs) > 0 {
		return needs[0].Key, false
	}
	if len(info) > 0 {
		return info[0].Key, false
	}
	return "", false
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

	reveal := a.access.RevealQueueProject == p.ID
	var targetKey string
	var targetAckErr bool
	if reveal {
		targetKey, targetAckErr = pickProjectQueueAlertTarget(q, needs, info, time.Now())
		q.LocatedNoticeKey = targetKey
	}

	groupDesc := ""
	if q.Loading {
		groupDesc = "更新中…"
	} else if q.AutomaticSave {
		groupDesc = "自动保存已开启"
	}
	var targetEl ui.Element
	hasTarget := false
	section := v4Group(c.Key("project-queue-section-"+p.ID), "消息与处理状态", groupDesc, func() {
		if q.Backlog != nil && (q.AutomaticSave || q.Backlog.active()) {
			queueAction(c, "暂停自动保存", func() { a.changeBacklog(q, p, true) }).Disabled(q.BacklogBusy || q.Backlog.Status == "stopping")
		}
		queueRefreshAction(c, "刷新消息状态", func() { a.loadProjectQueue(q, p); a.refreshBacklog(q, p) }).Disabled(q.Loading || q.BacklogLoading || a.access.Busy)
	}, func() {
		ui.Column(c.Key("message-status-"+p.ID)).FillWidth().Gap(0).Children(func() {
			if !q.Loaded && len(info) == 0 {
				v4Row(c, v4RowOptions{Title: "正在读取消息状态…"})
			}
			if el, ok := a.messageNoticeGroup(c, q, p, "需要你处理", "仅列出需要你操作的事项。", needs, true, targetKey, q.LocatedNoticeKey); ok {
				targetEl, hasTarget = el, true
			}
			if el, ok := a.messageNoticeGroup(c, q, p, "状态与异常提示", "无需你确认消息是否保存；系统异常会保留显示。", info, false, targetKey, q.LocatedNoticeKey); ok {
				targetEl, hasTarget = el, true
			}
			if len(history) > 0 {
				v4Row(c, v4RowOptions{
					Key:         "proj-notice-history-" + p.ID,
					Title:       fmt.Sprintf("已知晓的历史提示（%d）", len(history)),
					Clickable:   true,
					HideChevron: true,
					Control: func() {
						ico := v4IconChev
						if q.NoticeHistoryOpen {
							ico = v4IconDown
						}
						ui.Icon(c, ico).FontSize(14).TextColor(tok.Text3).Shrink(0)
					},
					OnClick: func() { q.NoticeHistoryOpen = !q.NoticeHistoryOpen },
				})
				if q.NoticeHistoryOpen {
					a.messageNoticeGroup(c, q, p, "已知晓的历史提示", "", history, false, "", "")
				}
			}
			if q.NoticeAcknowledgementError != "" {
				ackRow := ui.Row(c.Key("notice-ack-error-" + p.ID)).FillWidth().Padding(sp2, sp1)
				if reveal && targetAckErr {
					ackRow.Background(tok.Warning.Alpha(.07)).Radius(radius)
					targetEl, hasTarget = ackRow, true
				}
				ackRow.Children(func() {
					homeText(c, q.NoticeAcknowledgementError, homeBody).TextColor(tok.Warning)
				})
			}
			if q.Loaded && len(q.Documents) > 0 && q.DocumentError == "" {
				v4Row(c, v4RowOptions{
					Key:   "proj-queue-summary-" + p.ID,
					Title: "记忆整理",
					Desc:  "以下文档已保存，尚未完成记忆整理。",
					Control: func() {
						v4Badge(c, fmt.Sprintf("%d 份文档", len(q.Documents)), "accent")
					},
				})
				start := min(q.Offset, len(q.Documents))
				end := min(start+5, len(q.Documents))
				for _, item := range q.Documents[start:end] {
					item := item
					desc := "已保存 · 未完成整理"
					if item.Date != "" {
						desc = displayDate(item.Date) + " · " + desc
					}
					v4Row(c, v4RowOptions{
						Key:   "document-" + item.ID,
						Title: queuedDocumentTitle(item),
						Desc:  desc,
						Control: func() {
							queueAction(c, "查看", func() { a.openQueuedDocument(p, item) })
						},
					})
				}
				if len(q.Documents) > 5 {
					ui.Row(c).FillWidth().Padding(sp2, sp1).Gap(sp3).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(tok.Border).Children(func() {
						queueAction(c, "上一页", func() { q.Offset = max(0, q.Offset-5) }).Disabled(start == 0)
						homeText(c, fmt.Sprintf("%d–%d / %d", start+1, end, len(q.Documents)), homeMeta)
						queueAction(c, "下一页", func() { q.Offset += 5 }).Disabled(end == len(q.Documents))
					})
				}
			}
			if q.ArchiveError != "" {
				ui.Row(c).FillWidth().Padding(sp2, sp1).Children(func() {
					homeText(c, "历史异常记录读取失败；当前保存异常仍会显示。", homeMeta)
				})
			}
		})
	})
	if reveal {
		section.ScrollIntoView()
		if hasTarget {
			targetEl.ScrollIntoView()
		}
		if q.Loaded || !q.Loading {
			a.access.RevealQueueProject = ""
		}
	}
}

var messageAlertIcon = homeSVG(`<circle cx="12" cy="12" r="9"/><path d="M12 7v6m0 4h.01"/>`)
var messageClockIcon = homeSVG(`<circle cx="12" cy="12" r="9"/><path d="M12 7v5l3 2"/>`)
var messagePauseIcon = homeSVG(`<rect x="7" y="5" width="3" height="14" rx="1"/><rect x="14" y="5" width="3" height="14" rx="1"/>`)

func (a *App) messageNoticeGroup(c *ui.Context, q *projectQueueState, p AccessProject, title, subtitle string, rows []messageNotice, needsUser bool, targetKey, highlightKey string) (ui.Element, bool) {
	var targetEl ui.Element
	hasTarget := false
	ui.Column(c.Key(title)).FillWidth().Gap(0).Children(func() {
		if len(rows) == 0 {
			if q.Loaded {
				label := "当前没有需要你处理的事项"
				if !needsUser {
					label = "当前没有消息积压或已检测到的异常"
				}
				v4Row(c, v4RowOptions{
					Title: title,
					Desc:  label,
					Value: "正常",
				})
			}
			return
		}
		ui.Row(c).FillWidth().Padding(sp2, sp1, sp1, sp1).Gap(sp2).AlignItems(ui.Center).Children(func() {
			ui.Text(c, title).Font("system-ui").FontSize(fsCaption).FontWeight(600).TextColor(tok.Text3).SingleLine()
			ui.Text(c, fmt.Sprintf("%d", len(rows))).Font("system-ui").FontSize(fsCaption).TextColor(tok.Text3).SingleLine()
		})
		for _, n := range rows {
			n := n
			acknowledged := q.noticeAcknowledged(n.Key)
			row := ui.Row(c.Key(n.Key)).FillWidth().Padding(10, sp1).Gap(sp3).AlignItems(ui.Start).BorderWidth(0, 0, 1, 0).BorderColor(tok.Border)
			if highlightKey != "" && n.Key == highlightKey && !acknowledged {
				row.Background(tok.Warning.Alpha(.07)).Radius(radius)
			}
			if targetKey != "" && n.Key == targetKey {
				targetEl, hasTarget = row, true
			}
			row.Children(func() {
				color := tok.Text2
				icon := messageClockIcon
				switch n.Tag {
				case "系统异常", "读取异常", "整理异常", "保存受阻", "需要授权", "需要补交":
					color = tok.Warning
					icon = messageAlertIcon
				case "已暂停":
					icon = messagePauseIcon
				}
				ui.Column(c).Width(24).Height(24).Shrink(0).AlignItems(ui.Center).Justify(ui.Center).Background(color.Alpha(.08)).Radius(radius).Children(func() { ui.Icon(c, icon).FontSize(14).TextColor(color) })
				ui.Column(c).Grow(1).MinWidth(0).Gap(5).Children(func() {
					ui.Row(c).AlignItems(ui.Center).Gap(sp2).Children(func() {
						homeText(c, n.Title, homeBody).FontSize(fsBody).FontWeight(500).TextColor(tok.Text1)
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
							homeText(c, n.Source, homeMeta).FontSize(fsCaption).TextColor(tok.Text3)
						})
					}
					homeText(c, n.Detail, homeBody).FontSize(fsFootnote).TextColor(tok.Text2).LineHeight(1.45)
					if n.Followup != "" {
						homeText(c, n.Followup, homeMeta).LineHeight(1.45)
					}
					if n.Session != nil && n.Session.View.Message != "" && n.Session.View.ReadError == "" {
						homeText(c, n.Session.View.Message, homeMeta).LineHeight(1.45)
					}
					ui.Row(c).AlignItems(ui.Center).Gap(sp2).Children(func() {
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
		}
	})
	return targetEl, hasTarget
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
