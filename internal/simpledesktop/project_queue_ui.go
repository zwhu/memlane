package simpledesktop

import (
	"fmt"
	"strings"
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

var messageAlertIcon = homeSVG(`<circle cx="12" cy="12" r="9"/><path d="M12 7v6m0 4h.01"/>`)

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

func (q *projectQueueState) openDetailDialog(n messageNotice) {
	q.DetailNoticeKey = n.Key
	q.DetailDialogOpen = true
	q.DetailDiagnosticsOpen = false
	q.ProcessingActionError = ""
	if n.Session != nil && n.Session.View != nil {
		n.Session.View.CheckDetailsOpen = true
	}
}

func (q *projectQueueState) closeDetailDialog(n *messageNotice) {
	q.DetailDialogOpen = false
	q.DetailNoticeKey = ""
	q.DetailDiagnosticsOpen = false
	q.ProcessingActionError = ""
	if n != nil && n.Session != nil && n.Session.View != nil {
		n.Session.View.CheckDetailsOpen = false
	}
	for _, row := range q.Sessions {
		if row.View != nil {
			row.View.CheckDetailsOpen = false
		}
	}
}

func (q *projectQueueState) activeDetailNotice(needs, info, history []messageNotice) (messageNotice, bool) {
	all := make([]messageNotice, 0, len(needs)+len(info)+len(history))
	all = append(all, needs...)
	all = append(all, info...)
	all = append(all, history...)
	if q.DetailDialogOpen && q.DetailNoticeKey != "" {
		for _, n := range all {
			if n.Key == q.DetailNoticeKey {
				return n, true
			}
		}
	}
	for _, n := range all {
		if n.Session != nil && n.Session.View != nil && n.Session.View.CheckDetailsOpen {
			return n, true
		}
	}
	return messageNotice{}, false
}

func (a *App) projectQueueSection(c *ui.Context, p AccessProject) {
	q := a.projectQueue(p)
	needs, info := a.messageNotices(q, p)
	anomalies, progress := partitionInfoNotices(info)
	var resolvedProcessing []messageNotice
	now := time.Now()
	for _, failure := range q.ProcessingNotices {
		if failure.resolved() {
			resolvedProcessing = append(resolvedProcessing, processingMessageNotice(failure, now))
		}
	}
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
	needs, anomalies = active(needs), active(anomalies)

	reveal := a.access.RevealQueueProject == p.ID
	var targetKey string
	var targetAckErr bool
	if reveal {
		targetKey, targetAckErr = pickProjectQueueAlertTarget(q, needs, anomalies, time.Now())
		q.LocatedNoticeKey = targetKey
	}

	hasAlerts := len(needs) > 0 || len(anomalies) > 0
	hasDocs := q.Loaded && len(q.Documents) > 0 && q.DocumentError == ""
	hasProgress := len(progress) > 0 || hasDocs
	groupDesc := ""
	if hasAlerts && q.AutomaticSave {
		groupDesc = "自动采集已开启"
	}

	autoSaveControl := func(keySuffix, label string) {
		saveActive := q.AutomaticSave || q.Backlog.active()
		canStop := q.Backlog != nil && saveActive && q.Backlog.Status != "stopping"
		disabled := q.BacklogBusy || a.access.Busy || a.Config.ProjectControl == nil || (saveActive && !canStop)
		v4Switch(c, "proj-autosave-"+p.ID+"-"+keySuffix, &saveActive, label, func() {
			if disabled {
				return
			}
			a.changeBacklog(q, p, q.AutomaticSave || q.Backlog.active())
		}).Disabled(disabled)
	}

	var headerActions func()
	if hasAlerts {
		headerActions = func() {
			count := len(needs) + len(anomalies)
			ui.Text(c, fmt.Sprintf("%d 项待处理", count)).Font("system-ui").FontSize(fsCaption).FontWeight(500).FontFeatures("tnum").TextColor(tok.Warning).SingleLine()
			autoSaveControl("head", "自动采集")
			queueRefreshAction(c, "刷新", func() { a.loadProjectQueue(q, p); a.refreshBacklog(q, p) }).Disabled(q.Loading || q.BacklogLoading || a.access.Busy)
		}
	} else if !q.Loaded && !hasProgress {
		headerActions = func() {
			queueRefreshAction(c, "刷新", func() { a.loadProjectQueue(q, p); a.refreshBacklog(q, p) }).Disabled(q.Loading || q.BacklogLoading || a.access.Busy)
		}
	}

	var targetEl ui.Element
	hasTarget := false
	section := v4Group(c.Key("project-queue-section-"+p.ID), "消息与采集状态", groupDesc, headerActions, func() {
		ui.Column(c.Key("message-status-" + p.ID)).FillWidth().Gap(0).Children(func() {
			if !q.Loaded && !hasAlerts && !hasProgress {
				v4SkeletonRow(c, 1, false, false)
			} else if !hasAlerts {
				// Healthy collapsed single-row state.
				dotTone := "warn"
				title := "自动采集已暂停"
				if q.AutomaticSave {
					dotTone = "ok"
					title = "自动采集已开启 · 正常"
				} else if q.Backlog.active() {
					dotTone = "run"
					title = "正在采集消息"
				}
				v4Row(c, v4RowOptions{
					Key:   "proj-msg-healthy-" + p.ID,
					Title: title,
					Leading: func() {
						v4StatusDot(c, dotTone)
					},
					Control: func() {
						autoSaveControl("row", "")
						queueRefreshAction(c, "刷新", func() { a.loadProjectQueue(q, p); a.refreshBacklog(q, p) }).Disabled(q.Loading || q.BacklogLoading || a.access.Busy)
					},
				})
			} else {
				if len(needs) > 0 {
					if el, ok := a.messageNoticeBucket(c, q, p, "需要你处理", "warn", needs, targetKey, q.LocatedNoticeKey, true); ok {
						targetEl, hasTarget = el, true
					}
				}
				if len(anomalies) > 0 {
					if el, ok := a.messageNoticeBucket(c, q, p, "系统异常", "fail", anomalies, targetKey, q.LocatedNoticeKey, true); ok {
						targetEl, hasTarget = el, true
					}
				}
			}

			if q.NoticeAcknowledgementError != "" {
				ackRow := ui.Row(c.Key("notice-ack-error-"+p.ID)).FillWidth().Padding(sp2, sp1).BorderWidth(0, 0, 1, 0).BorderColor(tok.Border)
				if reveal && targetAckErr {
					ackRow.Background(tok.Warning.Alpha(.07)).Radius(radius)
					targetEl, hasTarget = ackRow, true
				}
				ackRow.Children(func() {
					homeText(c, q.NoticeAcknowledgementError, homeBody).TextColor(tok.Warning)
				})
			}

			if hasProgress {
				a.projectProgressBucket(c, q, p, progress, hasDocs)
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
					a.messageNoticeBucket(c, q, p, "", "", history, "", "", true)
				}
			}

			if len(resolvedProcessing) > 0 {
				v4Row(c, v4RowOptions{
					Key:         "proj-resolved-processing-" + p.ID,
					Title:       fmt.Sprintf("已恢复的提炼调用（%d）", len(resolvedProcessing)),
					Clickable:   true,
					HideChevron: true,
					Control: func() {
						ico := v4IconChev
						if q.ResolvedProcessingOpen {
							ico = v4IconDown
						}
						ui.Icon(c, ico).FontSize(fsCaption).TextColor(tok.Text3).SingleLine()
					},
					OnClick: func() { q.ResolvedProcessingOpen = !q.ResolvedProcessingOpen },
				})
				if q.ResolvedProcessingOpen {
					ui.Column(c.Key("resolved-processing-rows")).FillWidth().Gap(0).Children(func() {
						a.messageNoticeBucket(c, q, p, "", "", resolvedProcessing, "", "", false)
					})
				}
			}

			if q.ArchiveError != "" {
				ui.Row(c).FillWidth().Padding(sp2, sp1).Children(func() {
					homeText(c, "历史异常记录读取失败；当前采集异常仍会显示。", homeMeta)
				})
			}
		})
	})

	detailNotices := append(append([]messageNotice(nil), anomalies...), progress...)
	if activeNotice, ok := q.activeDetailNotice(needs, detailNotices, append(history, resolvedProcessing...)); ok {
		a.renderMessageNoticeDialog(c, q, p, activeNotice)
	}

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

func (a *App) projectProgressBucket(c *ui.Context, q *projectQueueState, p AccessProject, progress []messageNotice, hasDocs bool) {
	ui.Column(c.Key("proj-progress-" + p.ID)).FillWidth().Gap(0).Children(func() {
		ui.Row(c).FillWidth().Padding(sp2, sp1, sp1, sp1).Gap(sp2).AlignItems(ui.Center).Children(func() {
			v4StatusDot(c, "run")
			ui.Text(c, "进行中与待提炼").Font("system-ui").FontSize(fsCaption).FontWeight(600).TextColor(tok.Text3).SingleLine()
			if len(progress) > 0 && hasDocs {
				ui.Text(c, fmt.Sprintf("%d 项", len(progress))).Font("system-ui").FontSize(fsCaption).FontFeatures("tnum").TextColor(tok.Text3).SingleLine()
				ui.Text(c, "·").Font("system-ui").FontSize(fsCaption).TextColor(tok.Text3).SingleLine()
				ui.Text(c, fmt.Sprintf("%d 份文档", len(q.Documents))).Font("system-ui").FontSize(fsCaption).FontFeatures("tnum").TextColor(tok.Text3).SingleLine()
			} else if hasDocs {
				ui.Text(c, fmt.Sprintf("%d 份文档", len(q.Documents))).Font("system-ui").FontSize(fsCaption).FontFeatures("tnum").TextColor(tok.Text3).SingleLine()
			} else {
				ui.Text(c, fmt.Sprintf("%d", len(progress))).Font("system-ui").FontSize(fsCaption).FontFeatures("tnum").TextColor(tok.Text3).SingleLine()
			}
		})
		if len(progress) > 0 {
			a.messageNoticeBucket(c, q, p, "", "", progress, "", "", false)
		}
		if hasDocs {
			start := min(q.Offset, len(q.Documents))
			end := min(start+5, len(q.Documents))
			for _, item := range q.Documents[start:end] {
				item := item
				desc := "已采集 · 等待提炼"
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
	})
}

func (a *App) messageNoticeBucket(c *ui.Context, q *projectQueueState, p AccessProject, title, dotTone string, rows []messageNotice, targetKey, highlightKey string, allowAcknowledge bool) (ui.Element, bool) {
	var targetEl ui.Element
	hasTarget := false
	key := title
	if key == "" {
		if allowAcknowledge {
			key = "history-rows"
		} else {
			key = "progress-rows"
		}
	}
	ui.Column(c.Key(key)).FillWidth().Gap(0).Children(func() {
		if title != "" {
			ui.Row(c).FillWidth().Padding(sp2, sp1, sp1, sp1).Gap(sp2).AlignItems(ui.Center).Children(func() {
				if dotTone != "" {
					v4StatusDot(c, dotTone)
				}
				ui.Text(c, title).Font("system-ui").FontSize(fsCaption).FontWeight(600).TextColor(tok.Text3).SingleLine()
				ui.Text(c, fmt.Sprintf("%d", len(rows))).Font("system-ui").FontSize(fsCaption).FontFeatures("tnum").TextColor(tok.Text3).SingleLine()
			})
		}
		for _, n := range rows {
			n := n
			acknowledged := (n.Processing == nil || !n.Processing.resolved()) && q.noticeAcknowledged(n.Key)
			row := ui.Row(c.Key(n.Key)).FillWidth().Padding(10, sp1).Gap(sp3).AlignItems(ui.Start).BorderWidth(0, 0, 1, 0).BorderColor(tok.Border)
			if highlightKey != "" && n.Key == highlightKey && !acknowledged {
				row.Background(tok.Warning.Alpha(.07)).Radius(radius)
			}
			if targetKey != "" && n.Key == targetKey {
				targetEl, hasTarget = row, true
			}
			row.Children(func() {
				ui.Row(c).Padding(6, 0, 0, 0).Shrink(0).Children(func() {
					v4StatusDot(c, n.dotTone())
				})
				ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
					ui.Row(c).AlignItems(ui.Center).Gap(sp2).Children(func() {
						ui.Text(c, n.Title).Font("system-ui").FontSize(fsBody).FontWeight(500).TextColor(tok.Text1)
						if n.Tag != "" {
							v4Badge(c, n.Tag, n.badgeTone())
						}
						if acknowledged {
							label := "已知晓 · 不再提醒"
							if q.NoticeAcknowledgements[noticeHash(n.Key)].Deferred {
								label = "暂不处理 · 不再提醒"
							}
							homeText(c, label, homeMeta)
						}
					})
					if n.Source != "" {
						ui.Row(c).AlignItems(ui.Center).Gap(5).Children(func() {
							if n.Session != nil {
								agentLogo(c, accessAgentIcon(n.Session.Agent), 14)
							}
							ui.Text(c, n.Source).Font("system-ui").FontSize(fsCaption).TextColor(tok.Text3).SingleLine()
						})
					}
					if n.Detail != "" {
						ui.Text(c, n.Detail).Font("system-ui").FontSize(fsFootnote).TextColor(tok.Text2).LineHeight(1.45)
					}
					if n.Session == nil && n.Followup != "" {
						ui.Text(c, n.Followup).Font("system-ui").FontSize(fsCaption).TextColor(tok.Text3).LineHeight(1.4)
					}
					if n.Session != nil && n.Session.View.Message != "" && n.Session.View.ReadError == "" {
						ui.Text(c, n.Session.View.Message).Font("system-ui").FontSize(fsCaption).TextColor(tok.Text3).LineHeight(1.4)
					}
				})
				ui.Row(c).AlignItems(ui.Center).Gap(sp1).Shrink(0).Children(func() {
					if !acknowledged {
						switch n.Action {
						case "permissions":
							v4SmallPrimaryButton(c, "去授权", func() { q.Scroll.Y = 0 })
						case "replay":
							if n.Session != nil {
								row := *n.Session
								v4SmallPrimaryButton(c, "查看并补交", func() { q.openDetailDialog(n) }).Disabled(row.View.Busy || a.access.Busy || row.View.Status.Unknown > 0 || row.View.Status.Signature == "" || row.Agent.Installation == nil)
							}
						case "resume":
							v4SmallPrimaryButton(c, "恢复采集", func() { a.changeBacklog(q, p, false) }).Disabled(q.BacklogBusy || a.access.Busy || a.Config.ProjectControl == nil)
						case "save":
							v4SmallPrimaryButton(c, "开始采集", func() { a.changeBacklog(q, p, false) }).Disabled(q.BacklogBusy || q.Loading || a.access.Busy || a.Config.ProjectControl == nil)
						case "reconcile-policy":
							v4SmallPrimaryButton(c, "核对权限", func() { a.reconcileProjectPolicy(q, p) }).Disabled(a.access.Busy || a.Config.ProjectControl == nil)
						}
					}
					queueAction(c, "查看详情", func() { q.openDetailDialog(n) })
					if acknowledged {
						if allowAcknowledge {
							queueAction(c, "恢复提醒", func() { a.acknowledgeNotice(q, p, n, true) })
						}
					} else if allowAcknowledge {
						label := "知道了，不再提醒"
						if n.NeedsUser {
							label = "暂不处理"
						}
						queueAction(c, label, func() { a.acknowledgeNotice(q, p, n, false) }).Tooltip("仅结束这项提醒，保留原消息和异常记录；出现新的异常会再次提醒。")
					}
				})
			})
		}
	})
	return targetEl, hasTarget
}

func (a *App) renderMessageNoticeDialog(c *ui.Context, q *projectQueueState, p AccessProject, n messageNotice) {
	open := true
	diagCode := n.diagCode(q)
	hint := "原消息保留在本机，未确认前不会自动重发"
	if diagCode != "" {
		hint = "诊断码：" + diagCode
	} else if n.isActiveProgressNotice() {
		hint = "后台按队列顺序自动处理，切换页面不会中断"
	}

	var leading func()
	if n.Session != nil {
		agent := n.Session.Agent
		leading = func() { agentLogo(c, accessAgentIcon(agent), 20) }
	}

	confirmText := ""
	hideConfirm := true
	confirmDisabled := false
	var onConfirm func()

	switch n.Action {
	case "replay":
		if n.Session != nil {
			row := *n.Session
			hideConfirm = false
			confirmText = "确认补交这些消息"
			confirmDisabled = row.View.Busy || a.access.Busy || row.View.Status.Unknown > 0 || row.View.Status.Signature == "" || row.Agent.Installation == nil
			onConfirm = func() {
				a.runPendingAction(row.View, p, row.Agent, row.Binding, "replay")
				q.closeDetailDialog(&n)
			}
		}
	case "permissions":
		hideConfirm = false
		confirmText = "前往权限设置"
		onConfirm = func() {
			q.closeDetailDialog(&n)
			q.Scroll.Y = 0
		}
	case "resume":
		hideConfirm = false
		confirmText = "恢复采集"
		confirmDisabled = q.BacklogBusy || a.access.Busy || a.Config.ProjectControl == nil
		onConfirm = func() {
			a.changeBacklog(q, p, false)
			q.closeDetailDialog(&n)
		}
	case "save":
		hideConfirm = false
		confirmText = "开始采集"
		confirmDisabled = q.BacklogBusy || q.Loading || a.access.Busy || a.Config.ProjectControl == nil
		onConfirm = func() {
			a.changeBacklog(q, p, false)
			q.closeDetailDialog(&n)
		}
	case "reconcile-policy":
		hideConfirm = false
		confirmText = "核对权限"
		confirmDisabled = a.access.Busy || a.Config.ProjectControl == nil
		onConfirm = func() {
			a.reconcileProjectPolicy(q, p)
			q.closeDetailDialog(&n)
		}
	case "processing-retry":
		hideConfirm = false
		confirmText = "重新提炼"
		confirmDisabled = q.ProcessingActionBusy || a.access.Busy || a.Config.ProjectControl == nil
		onConfirm = func() {
			a.retryProjectProcessing(q, p)
		}
	case "refresh":
		hideConfirm = false
		confirmText = "刷新状态"
		confirmDisabled = q.Loading || q.BacklogLoading || a.access.Busy
		onConfirm = func() {
			a.loadProjectQueue(q, p)
			a.refreshBacklog(q, p)
			q.closeDetailDialog(&n)
		}
	default:
		if n.Session != nil {
			row := *n.Session
			check := q.pendingCheck(row)
			if check.Status == "unconfirmed" && check.Reason != "original_receipt_unavailable" {
				hideConfirm = false
				confirmText = "重新检查采集结果"
				confirmDisabled = row.View.Busy || a.access.Busy
				onConfirm = func() {
					a.retryPendingCheck(q, p, row)
				}
			}
		}
	}

	v4Dialog(c, &open, v4DialogOptions{
		Key:             "msg-detail-dialog-" + p.ID + "-" + noticeHash(n.Key),
		Title:           n.Title,
		Width:           v4DialogLarge,
		ScrollBody:      true,
		Leading:         leading,
		CancelText:      "关闭",
		ConfirmText:     confirmText,
		HideConfirm:     hideConfirm,
		ConfirmDisabled: confirmDisabled,
		Hint:            hint,
		OnCancel:        func() { q.closeDetailDialog(&n) },
		OnConfirm:       onConfirm,
		Body: func() {
			// Top summary strip: session/source, tag, diagnostic code chip.
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(sp2).Children(func() {
				if n.Source != "" {
					ui.Text(c, n.Source).Font("system-ui").FontSize(fsFootnote).FontWeight(500).TextColor(tok.Text1)
				}
				if n.Tag != "" {
					v4Badge(c, n.Tag, n.badgeTone())
				}
				if diagCode != "" {
					ui.Text(c, diagCode).Font("ui-monospace").FontSize(fsCaption).FontFeatures("tnum").
						Padding(2, 6).Radius(radiusSmall).Background(tok.Fill2).TextColor(tok.Text2).SingleLine()
				}
				ui.Spacer(c)
				if n.Session != nil {
					if check := q.pendingCheck(*n.Session); check.CheckedAt > 0 {
						ui.Text(c, "检查于 "+time.Unix(int64(check.CheckedAt), 0).Format("01月02日 15:04")).
							Font("system-ui").FontSize(fsCaption).FontFeatures("tnum").TextColor(tok.Text3).SingleLine()
					}
				}
			})

			// Safety callout (borderless soft fill, zero shadow).
			safetyLines := []string{}
			if n.Detail != "" {
				safetyLines = append(safetyLines, n.Detail)
			}
			if n.Followup != "" {
				safetyLines = append(safetyLines, n.Followup)
			}
			if len(safetyLines) > 0 {
				ui.Row(c).FillWidth().Padding(sp2, sp3).Gap(sp2).AlignItems(ui.Start).
					Radius(radius).Background(tok.Fill1).Border(1, tok.Border).Children(func() {
					calloutDot := "warn"
					if n.isProgressNotice() {
						calloutDot = n.dotTone()
					}
					ui.Row(c).Padding(5, 0, 0, 0).Shrink(0).Children(func() {
						v4StatusDot(c, calloutDot)
					})
					ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
						for _, line := range safetyLines {
							ui.Text(c, line).Font("system-ui").FontSize(fsFootnote).TextColor(tok.Text2).LineHeight(1.5)
						}
					})
				})
			}

			if n.Session != nil {
				a.messageNoticeDetails(c, q, p, *n.Session)
			} else if n.Processing != nil {
				a.processingNoticeDetails(c, q, *n.Processing)
			} else if n.Key == "processing-failed" || n.Key == "processing-active" {
				a.backlogProcessingDetails(c, q, p, n.Key)
			}
		},
	})
	if !open {
		q.closeDetailDialog(&n)
	}
}

func (a *App) messageNoticeDetails(c *ui.Context, q *projectQueueState, p AccessProject, row queuedSession) {
	v := row.View
	check := q.pendingCheck(row)
	ui.Column(c).FillWidth().Gap(sp3).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(sp2).Children(func() {
			ui.Text(c, "会话："+shortSessionLabel(row.Binding.HostSessionKey)).
				Font("system-ui").FontSize(fsCaption).TextColor(tok.Text3).Tooltip(row.Binding.HostSessionKey)
			ui.Spacer(c)
			if v.Loaded && v.Status.Pending > 0 {
				if row.Agent.Installation != nil && row.Agent.Installation.HostKind != "opencode" {
					queueAction(c, "查看本机消息", func() { a.runPendingAction(v, p, row.Agent, row.Binding, "details") }).Disabled(v.Busy || a.access.Busy)
				}
			}
		})

		if v.Status.Blocked != nil {
			ui.Text(c, "暂停原因："+backlogReason(v.Status.Blocked.Reason)).Font("system-ui").FontSize(fsFootnote).TextColor(tok.Text1)
		}
		if v.Loaded && v.Status.Pending > 0 && (row.Agent.Installation == nil || row.Agent.Installation.HostKind == "opencode") {
			ui.Text(c, "当前接入暂不支持读取本机消息摘要。").Font("system-ui").FontSize(fsCaption).TextColor(tok.Text3)
		}
		if v.DetailsError != "" {
			ui.Text(c, v.DetailsError).Font("system-ui").FontSize(fsFootnote).TextColor(tok.Warning)
		}

		// Local message summaries (hairline-separated list, zero nested cards).
		if v.DetailsLoaded {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(sp2).Children(func() {
				ui.Text(c, "本地消息摘要").Font("system-ui").FontSize(fsCaption).FontWeight(600).TextColor(tok.Text3).SingleLine()
				ui.Text(c, fmt.Sprintf("已读取 %d 条 · 最多 20 条", len(v.Details))).Font("system-ui").FontSize(fsCaption).FontFeatures("tnum").TextColor(tok.Text3).SingleLine()
			})
			visible := len(v.Details)
			if !v.AllMessagesOpen {
				visible = min(5, visible)
			}
			ui.Column(c).FillWidth().Gap(0).BorderWidth(1, 0, 0, 0).BorderColor(tok.Border).Children(func() {
				for index, entry := range v.Details[:visible] {
					entry := entry
					key := fmt.Sprintf("%s/%d", entry.ID, index)
					ui.Row(c.Key(key)).FillWidth().Padding(sp2, sp1).Gap(sp2).AlignItems(ui.Start).
						BorderWidth(0, 0, 1, 0).BorderColor(tok.Border).Children(func() {
						ui.Text(c, fmt.Sprintf("#%02d", index+1)).Font("ui-monospace").FontSize(fsCaption).FontFeatures("tnum").TextColor(tok.Text3).Width(28).Shrink(0)
						ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
							ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(sp2).Children(func() {
								ui.Text(c, entry.Kind).Font("system-ui").FontSize(fsFootnote).FontWeight(500).TextColor(tok.Text1)
								if entry.Unknown {
									v4Badge(c, "结果未确认", "warn")
								} else {
									v4Badge(c, "待采集", "accent")
								}
								ui.Spacer(c)
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
								ui.Text(c, entry.Summary).Font("system-ui").FontSize(fsFootnote).TextColor(tok.Text1).LineHeight(1.55)
							} else {
								ui.Text(c, entry.Summary).Font("system-ui").FontSize(fsFootnote).TextColor(tok.Text1).SingleLine().Tooltip(entry.Summary)
							}
							if v.DiagnosticsOpen {
								ui.Text(c, "消息标识："+entry.ID).Font("ui-monospace").FontSize(fsCaption).TextColor(tok.Text3).SingleLine().Tooltip(entry.ID)
							}
						})
					})
				}
			})
			if len(v.Details) > 5 {
				label := fmt.Sprintf("显示其余 %d 条", len(v.Details)-5)
				if v.AllMessagesOpen {
					label = "收起为 5 条"
				}
				ui.Row(c).Children(func() {
					queueAction(c, label, func() { v.AllMessagesOpen = !v.AllMessagesOpen })
				})
			}
			if len(v.Details) == 0 {
				ui.Text(c, "当前未读取到消息摘要，请刷新状态。").Font("system-ui").FontSize(fsCaption).TextColor(tok.Text3)
			}
		}

		// Collapsible SF Mono technical diagnostics block.
		diagLines := []string{"session_id=" + row.Binding.HostSessionKey}
		if v.ReadCode != "" {
			diagLines = append(diagLines, "read_code="+v.ReadCode)
		}
		if check.Reason != "" && check.Reason != v.ReadCode {
			diagLines = append(diagLines, "check_reason="+check.Reason)
		}
		if len(v.Status.UnknownEntryIDs) > 0 {
			diagLines = append(diagLines, fmt.Sprintf("unknown_ids=%d", len(v.Status.UnknownEntryIDs)))
		}
		diagCopyText := strings.Join(diagLines, "\n")

		ui.Column(c).FillWidth().Gap(sp2).Padding(sp2, 0, 0, 0).BorderWidth(1, 0, 0, 0).BorderColor(tok.Border).Children(func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(sp2).Children(func() {
				label := "诊断信息"
				if v.DiagnosticsOpen {
					label = "收起诊断信息"
				}
				queueAction(c, label, func() { v.DiagnosticsOpen = !v.DiagnosticsOpen })
				ui.Spacer(c)
				v4SmallButton(c, "复制诊断", v4IconCopy, func() {
					c.WriteClipboard(diagCopyText)
					a.copyFeedback("诊断信息")
				})
			})
			if v.DiagnosticsOpen {
				ui.Column(c).FillWidth().Padding(sp2, sp3).Gap(4).Background(tok.Fill1).Radius(radius).Children(func() {
					ui.Text(c, "会话："+row.Binding.HostSessionKey).Font("ui-monospace").FontSize(fsCaption).TextColor(tok.Text2)
					if v.ReadCode != "" {
						ui.Text(c, "诊断代码："+v.ReadCode).Font("ui-monospace").FontSize(fsCaption).TextColor(tok.Text2)
					}
					if check.Reason != "" && check.Reason != v.ReadCode {
						ui.Text(c, "诊断代码："+check.Reason).Font("ui-monospace").FontSize(fsCaption).TextColor(tok.Text2)
					}
					if len(v.Status.UnknownEntryIDs) > 0 {
						ui.Text(c, fmt.Sprintf("%d 条消息有本机标识；标识不代表服务端已采集。", len(v.Status.UnknownEntryIDs))).
							Font("ui-monospace").FontSize(fsCaption).TextColor(tok.Text3)
					}
				})
			}
		})
	})
}

func (a *App) processingNoticeDetails(c *ui.Context, q *projectQueueState, f processingNotice) {
	type diagEntry struct {
		key   string
		value string
	}
	var entries []diagEntry
	if f.Code != "" {
		entries = append(entries, diagEntry{"code", f.Code})
	}
	if f.Status > 0 {
		entries = append(entries, diagEntry{"http_status", fmt.Sprintf("%d", f.Status)})
	}
	if f.Backend != "" {
		entries = append(entries, diagEntry{"backend", f.Backend})
	}
	if f.Operation != "" {
		entries = append(entries, diagEntry{"operation_id", f.Operation})
	}
	if f.Request != "" {
		entries = append(entries, diagEntry{"request_id", f.Request})
	}
	if f.At != "" {
		entries = append(entries, diagEntry{"at", f.At})
	}
	if f.RecoveredAt != "" {
		entries = append(entries, diagEntry{"recovered_at", f.RecoveredAt})
	}
	if f.ResolvedAt != "" {
		entries = append(entries, diagEntry{"resolved_at", f.ResolvedAt})
	}
	if f.FailedAt != "" {
		entries = append(entries, diagEntry{"failed_at", f.FailedAt})
	}
	if len(entries) == 0 {
		return
	}
	diagLines := make([]string, 0, len(entries))
	for _, entry := range entries {
		diagLines = append(diagLines, entry.key+"="+entry.value)
	}
	diagCopyText := strings.Join(diagLines, "\n")

	ui.Column(c).FillWidth().Gap(sp2).Padding(sp2, 0, 0, 0).BorderWidth(1, 0, 0, 0).BorderColor(tok.Border).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(sp2).Children(func() {
			label := "诊断信息"
			if q.DetailDiagnosticsOpen {
				label = "收起诊断信息"
			}
			queueAction(c, label, func() { q.DetailDiagnosticsOpen = !q.DetailDiagnosticsOpen })
			ui.Spacer(c)
			v4SmallButton(c, "复制诊断", v4IconCopy, func() {
				c.WriteClipboard(diagCopyText)
				a.copyFeedback("诊断信息")
			})
		})
		if q.DetailDiagnosticsOpen {
			ui.Column(c).FillWidth().Padding(sp2, sp3).Gap(4).Background(tok.Fill1).Radius(radius).Children(func() {
				for _, line := range diagLines {
					ui.Text(c, line).Font("ui-monospace").FontSize(fsCaption).TextColor(tok.Text2)
				}
			})
		}
	})
}

func (a *App) agentNameForID(agentID string) string {
	for _, agent := range a.access.Directory.Agents {
		if agent.ID == agentID && agent.Name != "" {
			return agentDisplayName(agent.Name)
		}
	}
	return agentID
}

func processingIssueBadge(issue processingIssue) (label, tone string) {
	if issue.ReconcileStatus == "running" {
		return "服务端处理中", "accent"
	}
	if issue.Processing == "submitting" {
		return "处理中", "accent"
	}
	return "未确认", "warn"
}

func processingIssueSummary(issue processingIssue) string {
	if issue.ReconcileStatus == "running" {
		return "官方引擎仍在执行该会话的提炼任务，后台将继续对账，不会重复提交。"
	}
	switch issue.Reason {
	case "policy_unconfirmed":
		return "发起提炼前未能确认项目权限状态；原消息已保存，可核对后重新提炼。"
	case "owner_interrupted":
		return "后台服务重启或停止打断了本次提交；可先核对服务端状态或重新提炼。"
	case "previous_submission_unconfirmed":
		return "同会话此前存在未确认的提炼提交，因此本次暂缓重复发送。"
	default:
		if issue.Processing == "submitting" {
			return "已发起记忆提炼提交，正在等待服务端回执。"
		}
		if issue.ReconcileStatus == "failed" {
			return "核对显示最近一次提炼在服务端未能完成，可点击重新提炼。"
		}
		return "未能确认提炼请求是否已送达服务端，核对前不会自动重发。"
	}
}

func (a *App) backlogProcessingDetails(c *ui.Context, q *projectQueueState, p AccessProject, noticeKey string) {
	issues := make([]processingIssue, 0, len(q.ProcessingIssues))
	for _, issue := range q.ProcessingIssues {
		if noticeKey == "processing-active" && issue.Processing == "submitting" {
			issues = append(issues, issue)
		} else if noticeKey == "processing-failed" && (issue.Processing == "unknown" || issue.Processing == "failed") {
			issues = append(issues, issue)
		}
	}
	ui.Column(c).FillWidth().Gap(sp3).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(sp2).Children(func() {
			ui.Text(c, "相关会话").Font("system-ui").FontSize(fsCaption).FontWeight(600).TextColor(tok.Text3).SingleLine()
			if len(issues) > 0 {
				ui.Text(c, fmt.Sprintf("%d 个会话", len(issues))).Font("system-ui").FontSize(fsCaption).FontFeatures("tnum").TextColor(tok.Text3).SingleLine()
			}
			ui.Spacer(c)
			if noticeKey == "processing-failed" {
				label := "核对提炼状态"
				if q.ProcessingActionBusy {
					label = "正在核对…"
				}
				queueAction(c, label, func() { a.reconcileProjectProcessing(q, p) }).Disabled(q.ProcessingActionBusy || a.access.Busy || a.Config.ProjectControl == nil)
			}
		})

		if q.ProcessingActionError != "" {
			ui.Text(c, q.ProcessingActionError).Font("system-ui").FontSize(fsFootnote).TextColor(tok.Warning)
		}

		if len(issues) == 0 {
			emptyText := "当前未读取到进行中的会话明细，可稍后刷新查看。"
			if noticeKey == "processing-failed" {
				emptyText = "当前未读取到具体会话明细，可点击「核对提炼状态」同步最新服务端状态。"
			}
			ui.Text(c, emptyText).Font("system-ui").FontSize(fsCaption).TextColor(tok.Text3)
			return
		}

		diagLines := make([]string, 0, len(issues))
		ui.Column(c).FillWidth().Gap(0).BorderWidth(1, 0, 0, 0).BorderColor(tok.Border).Children(func() {
			for idx, issue := range issues {
				issue := issue
				badgeLabel, badgeTone := processingIssueBadge(issue)
				source := a.agentNameForID(issue.AgentID) + " · 会话 " + shortSessionLabel(issue.Host)
				if issue.AttemptedAt > 0 {
					source += " · 提交于 " + time.Unix(int64(issue.AttemptedAt), 0).Format("01/02 15:04")
				}
				diagLines = append(diagLines, fmt.Sprintf(
					"agent=%s host=%s session=%s status=%s reason=%s reconcile=%s",
					issue.AgentID, issue.Host, issue.SessionID, issue.Processing, issue.Reason, issue.ReconcileStatus,
				))
				ui.Row(c.Key(fmt.Sprintf("%s/%s/%d", issue.AgentID, issue.Host, idx))).FillWidth().Padding(sp2, sp1).Gap(sp2).AlignItems(ui.Start).
					BorderWidth(0, 0, 1, 0).BorderColor(tok.Border).Children(func() {
					ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
						ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(sp2).Children(func() {
							ui.Text(c, source).Font("system-ui").FontSize(fsFootnote).FontWeight(500).TextColor(tok.Text1).Tooltip(issue.Host)
							v4Badge(c, badgeLabel, badgeTone)
							ui.Spacer(c)
							if issue.ReconciledAt > 0 {
								ui.Text(c, "核对于 "+time.Unix(int64(issue.ReconciledAt), 0).Format("15:04")).
									Font("system-ui").FontSize(fsCaption).FontFeatures("tnum").TextColor(tok.Text3).SingleLine()
							}
						})
						ui.Text(c, processingIssueSummary(issue)).Font("system-ui").FontSize(fsFootnote).TextColor(tok.Text2).LineHeight(1.45)
					})
				})
			}
		})

		diagCopyText := strings.Join(diagLines, "\n")
		ui.Column(c).FillWidth().Gap(sp2).Padding(sp2, 0, 0, 0).BorderWidth(1, 0, 0, 0).BorderColor(tok.Border).Children(func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(sp2).Children(func() {
				label := "诊断信息"
				if q.DetailDiagnosticsOpen {
					label = "收起诊断信息"
				}
				queueAction(c, label, func() { q.DetailDiagnosticsOpen = !q.DetailDiagnosticsOpen })
				ui.Spacer(c)
				v4SmallButton(c, "复制诊断", v4IconCopy, func() {
					c.WriteClipboard(diagCopyText)
					a.copyFeedback("诊断信息")
				})
			})
			if q.DetailDiagnosticsOpen {
				ui.Column(c).FillWidth().Padding(sp2, sp3).Gap(4).Background(tok.Fill1).Radius(radius).Children(func() {
					for _, line := range diagLines {
						ui.Text(c, line).Font("ui-monospace").FontSize(fsCaption).TextColor(tok.Text2)
					}
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
