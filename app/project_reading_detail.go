package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"github.com/zwhu/memlane/app/reading"
	"github.com/zwhu/memlane/app/settings"
)

func projectDeleteTitle(i Item) string {
	title := strings.TrimSpace(projectDisplayTitle(i))
	if title == "" {
		return "这条记忆"
	}
	runes := []rune(title)
	if len(runes) > 28 {
		return string(runes[:28]) + "…"
	}
	return title
}

func projectDisplayTitle(i Item) string {
	if i.Session != nil {
		if sessionHasTopic(i) {
			return strings.TrimSpace(i.Title)
		}
		return sessionFallbackTitle(i)
	}
	if i.Kind == "知识" {
		if i.Document != nil {
			return readableDocumentTitle(*i.Document)
		}
		return ""
	}
	return itemPresentation(i).DisplayTitle
}
func (a *App) projectDetail(c *ui.Context, s *pane) {
	ui.Column(c.Key("project-detail-panel")).Grow(1).MinWidth(0).MinHeight(0).Background(tok.Bg).Children(func() {
		if s.Selected == nil {
			switch {
			case s.Locating:
				v4EmptyState(c, "reader-locating", "正在定位记忆…", "")
			case !s.Loaded && s.Loading && s.Error == "":
				ui.Column(c).Grow(1).MinHeight(0).FillWidth()
			case a.kind != "知识":
				v4EmptyState(c, "reader-empty", "选择一条原始记录", s.Error)
			default:
				v4EmptyState(c, "reader-empty", "选择一条记忆", s.Error)
			}
			appFooter(c, "项目阅读栏").Padding(0, sp6).Children(func() {
				if a.kind == "知识" {
					homeText(c, "Cognee 提供", homeMeta)
				} else {
					homeText(c, "Cognee 提供 · 只读", homeMeta)
				}
			})
			return
		}
		i := s.Selected
		key := i.Key()
		paged := i.Detail != nil && sessionHistoryFor(s, *i) != nil
		scrollKey := key
		if i.Session != nil && s.SessionSubagents && len(i.Subagents) > 0 {
			scrollKey += "/subagents"
		} else if i.Session != nil && s.SessionTools {
			scrollKey += "/tools"
		}
		scroll := projectDetailScroll(s, scrollKey)
		if a.motionRuns.docScrollKey != key {
			a.motionRuns.docScrollKey = key
			a.motionRuns.docEnter++
		}
		bodyLoading := !i.BodyLoaded && i.Detail == nil && i.Preview == "" && i.Body == "" && s.Reading
		bodyState := scrollKey + ":ready"
		if bodyLoading {
			bodyState = scrollKey + ":loading"
		}
		if a.motionRuns.docBodyKey != bodyState {
			a.motionRuns.docBodyKey = bodyState
			if !bodyLoading {
				a.motionRuns.docBodyFade++
			}
		}
		if !bodyLoading && i.Detail != nil && paged {
			a.checkSessionAutoLoad(s, *i, scroll)
		}

		headerBorder := ui.Transparent
		if paged || scroll.Y > 2 {
			headerBorder = tok.Border
		}
		ui.Column(c.Key("project-detail-header")).FillWidth().Shrink(0).Padding(sp6, sp10, sp2, sp10).
			BorderWidth(0, 0, 1, 0).BorderColor(headerBorder).Children(func() {
			ui.Column(c).FillWidth().MaxWidth(readerMaxW).Gap(sp3).Children(func() {
				a.projectDetailHeader(c, s, i, paged)
				if i.BodyTruncated {
					homeText(c, "正文超过 8 MiB，已截断显示", homeMeta)
				}
			})
		})

		fadeKey := fmt.Sprintf("project-detail-body-%d", a.motionRuns.docBodyFade)
		bodyHost := ui.Column(c.Key(fadeKey)).Grow(1).MinHeight(0).FillWidth()
		if !bodyLoading {
			a.v4ContentFade(c, bodyHost, "detail-body", a.motionRuns.docBodyFade)
		}
		bodyHost.Children(func() {
			switch {
			case bodyLoading:
				ui.Column(c).Grow(1).FillWidth().Padding(sp3, sp10, sp8, sp10).Children(func() {
					ui.Column(c).FillWidth().MaxWidth(readerMaxW).Children(func() {
						v4SkeletonGroup(c, 3, 2, false, false)
					})
				})
			case sessionHistoryMissing(*i) && !(paged && s.SessionSubagents && len(i.Subagents) > 0):
				a.renderMissingSessionHistory(c, s, *i, scrollKey, scroll)
			case i.Detail != nil:
				rows := buildSessionDetailRows(a, s, *i, paged)
				a.renderDetailRowList(c, s, *i, scrollKey, scroll, rows)
			default:
				presentation := itemPresentation(*i)
				if shouldVirtualizeMarkdown(presentation.VisibleBody) {
					rows := buildDocumentDetailRows(a, *i, presentation)
					a.renderDetailRowList(c, s, *i, scrollKey, scroll, rows)
				} else {
					ui.Scroll(c.Key("project-detail-scroll-"+scrollKey)).Grow(1).MinHeight(0).FillWidth().TrackScroll(scroll).Padding(sp3, sp10, sp8, sp10).Children(func() {
						ui.Column(c).FillWidth().MaxWidth(readerMaxW).Gap(sp4).Children(func() {
							markdown.View(c.Key("body-"+key), presentation.VisibleBody, a.v4MarkdownOptions(fsReading))
							if presentation.Unrecognized {
								ui.Text(c, "原文格式未识别，保留原文").FontSize(fsCaption).TextColor(tok.Text3)
							}
							a.projectDetailTail(c, s, i)
						})
					})
				}
			}
		})

		_, canCopy := reading.CopyBody(i.Body, !sessionHistoryMissing(*i) && (i.BodyLoaded || i.Detail != nil || (i.Document == nil && i.Preview != "")), i.ReadingState == "unavailable")
		appFooter(c, "项目阅读栏").Padding(0, sp6).Gap(sp2).Children(func() {
			if i.Kind == "知识" {
				homeText(c, "Cognee 提供", homeMeta)
			} else {
				homeText(c, "Cognee 提供 · 只读", homeMeta)
			}
			ui.Spacer(c)
			if time.Now().Before(a.projectUI.CopyUntil) {
				homeText(c, a.projectUI.Copied+"已复制", homeMeta)
			}
			v4OutlineButton(c, "复制正文", v4IconCopy, func() {
				if s.Selected == nil || s.Selected.Key() != i.Key() || s.Selected.ReadingState == "unavailable" {
					return
				}
				c.WriteClipboard(sessionCopyBody(s, *s.Selected))
				a.copyFeedback("正文")
			}).Disabled(!canCopy)
		})
		a.projectDeleteDialog(c, s, *i)
	})
}

func (a *App) projectDeleteDialog(c *ui.Context, s *pane, i Item) {
	if a.projectUI.DeleteTarget == nil || a.projectUI.DeleteTarget.Key() != i.Key() {
		return
	}
	open := true
	target := *a.projectUI.DeleteTarget
	v4Dialog(c, &open, v4DialogOptions{
		Key:             "project-memory-delete-" + target.Key(),
		Title:           fmt.Sprintf("删除『%s』？", projectDeleteTitle(target)),
		Width:           v4DialogSmall,
		Danger:          true,
		CancelText:      "保留记忆",
		CancelDisabled:  a.projectUI.DeleteBusy,
		ConfirmText:     "确认删除",
		ConfirmLabel:    "确认删除记忆",
		ConfirmDisabled: a.projectUI.DeleteBusy,
		OnCancel: func() {
			if !a.projectUI.DeleteBusy {
				a.projectUI.DeleteTarget = nil
				a.projectUI.DeleteError = ""
			}
		},
		OnConfirm: func() {
			a.deleteProjectKnowledge(s, target)
		},
		Body: func() {
			ui.Text(c, "产生这条记忆的原始对话会保留。").Font("system-ui").FontSize(fsBody).TextColor(tok.Text2).LineHeight(1.6)
			if a.projectUI.DeleteError != "" {
				ui.Text(c, a.projectUI.DeleteError).Font("system-ui").FontSize(fsCaption).TextColor(tok.Danger).LineHeight(1.5)
			}
		},
	})
	if !open && !a.projectUI.DeleteBusy {
		a.projectUI.DeleteTarget = nil
		a.projectUI.DeleteError = ""
	}
}

func (a *App) deleteProjectKnowledge(s *pane, target Item) {
	if a.projectUI.DeleteBusy || target.Document == nil || target.Dataset == "" || target.ID == "" {
		return
	}
	control, ok := a.accessClient()
	if !ok {
		a.projectUI.DeleteError = "控制面服务不可用，无法删除记忆"
		return
	}
	if !a.syncReadingIdentity() || a.projectUI.DeleteTarget == nil || a.projectUI.DeleteTarget.Key() != target.Key() {
		return
	}
	a.projectUI.DeleteBusy = true
	a.projectUI.DeleteError = ""
	a.projectUI.DeleteGeneration++
	gen := a.projectUI.DeleteGeneration
	authEpoch := a.authEpoch
	identity := a.cachedBaseReadingIdentity()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		client, err := newAppAPIClient(control, identity)
		if err == nil {
			err = client.DeleteKnowledgeDocument(ctx, target.Dataset, target.ID)
		}
		a.Update(func() {
			if gen != a.projectUI.DeleteGeneration {
				return
			}
			a.projectUI.DeleteBusy = false
			if authEpoch != a.authEpoch || !a.syncReadingIdentity() {
				return
			}
			if err != nil {
				msg := settings.ControlUserError(err)
				if msg == "" {
					msg = err.Error()
				}
				if readingUnavailable(err) {
					a.InvalidateReading(target.Dataset)
					if s != nil {
						s.Error = "删除失败：" + msg
					}
				}
				a.projectUI.DeleteError = msg
				return
			}
			a.projectUI.DeleteTarget = nil
			a.projectUI.DeleteError = ""
			a.applyKnowledgeDocumentDeleted(target, true)
			if s != nil && a.page == "知识" && a.kind == "知识" {
				if cur, ok := a.current(); ok && cur.Dataset == target.Dataset {
					a.load(s)
				}
			}
		})
	}()
}

func (a *App) projectDetailHeader(c *ui.Context, s *pane, i *Item, paged bool) {
	key := i.Key()
	if sourceSession(s) {
		a.sourceContext(c, s)
	}
	a.subagentContext(c, s, *i)
	ui.Row(c).Label("项目内容工具栏").FillWidth().MinHeight(28).Padding(0, 0, sp4, 0).Gap(sp2).AlignItems(ui.Center).
		BorderWidth(0, 0, 1, 0).BorderColor(tok.Border).Children(func() {
		agentLogo(c, recentAgentIcon(*i), 18)
		status := sessionSourceAgentDisplay(*i)
		if p, ok := a.current(); ok && p.Name != "" {
			status += " · " + p.Name
		}
		status += " · " + displayDate(i.Date)
		row := ui.Row(c).MinWidth(0).AlignItems(ui.Center).Gap(4)
		row.Children(func() {
			homeText(c, status, homeBody).SingleLine()
			hoverCopyAction(c, row, "复制内容", func() { c.WriteClipboard(sessionCopyBody(s, *i)); a.copyFeedback("内容") }).Disabled(!(i.BodyLoaded || i.Detail != nil || i.Preview != ""))
		})
		knowledgeRecallLabel(c, *i)
		knowledgeBasisTag(c, *i)
		ui.Spacer(c)
		anchor := readingAction(c, "内容信息", readingInfo, func() {
			a.projectUI.InfoOpen = !a.projectUI.InfoOpen
			a.projectUI.InfoKey = key
			a.projectUI.MenuOpen = false
		})
		if a.projectUI.InfoKey != key {
			a.projectUI.InfoOpen = false
		}
		a.contentInformation(c, anchor, *i)
		if i.Kind == "知识" && i.Document != nil && a.Config.ProjectControl != nil && i.ReadingState != "unavailable" {
			v4DangerOutlineButton(c, "删除", true, func() {
				itemCopy := *i
				a.projectUI.InfoOpen = false
				a.projectUI.DeleteTarget = &itemCopy
				a.projectUI.DeleteError = ""
			}).Label("删除记忆").Disabled(a.projectUI.DeleteBusy)
		}
	})
	title := projectDisplayTitle(*i)
	if title != "" {
		ui.Column(c).FillWidth().Gap(4).Children(func() {
			t := homeText(c, title, homeHeading).FontSize(fsTitle).LineHeight(1.35).MinWidth(0)
			if !a.projectUI.Expanded[key] {
				t.MaxLines(3)
			}
			if i.Session != nil {
				if idLabel := sessionIdentity(i.ID); idLabel != title {
					homeText(c, idLabel, homeMeta)
				}
			}
			if len([]rune(title)) > 80 {
				label := "展开完整标题"
				if a.projectUI.Expanded[key] {
					label = "收起标题"
				}
				projectLink(c, label, func() {
					if a.projectUI.Expanded == nil {
						a.projectUI.Expanded = map[string]bool{}
					}
					a.projectUI.Expanded[key] = !a.projectUI.Expanded[key]
				})
			}
		})
	}
	if s.Error != "" {
		homeText(c, "内容未刷新 · "+s.Error, homeMeta)
	}
	if paged && (!sessionHistoryMissing(*i) || len(i.Subagents) > 0) {
		a.sessionHistoryControls(c, s, *i)
	}
}

func (a *App) renderMissingSessionHistory(c *ui.Context, s *pane, i Item, scrollKey string, scroll *ui.ScrollState) {
	agentLabel := sessionSourceAgentDisplay(i)
	ui.Scroll(c.Key("project-detail-scroll-"+scrollKey)).Grow(1).MinHeight(0).FillWidth().TrackScroll(scroll).Padding(sp4, sp10, sp8, sp10).Children(func() {
		ui.Column(c).FillWidth().MaxWidth(readerMaxW).Gap(sp4).Children(func() {
			ui.Column(c).FillWidth().Padding(sp4).Gap(sp3).Radius(radius).Background(tok.Fill1).Border(1, tok.Border).Children(func() {
				ui.Text(c, "未找到对应原始会话原文（可能已过期或未留存）").Font("system-ui").FontSize(fsBody).FontWeight(550).TextColor(tok.Text1).LineHeight(1.5)
				ui.Column(c).FillWidth().Gap(sp2).Children(func() {
					ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(sp2).Children(func() {
						homeText(c, "历史会话 ID", homeMeta).Shrink(0)
						row := ui.Row(c).MinWidth(0).AlignItems(ui.Center).Gap(4)
						row.Children(func() {
							ui.Text(c, i.ID).Font("ui-monospace").FontSize(fsFootnote).TextColor(tok.Text1).SingleLine().MinWidth(0).Tooltip(i.ID)
							hoverCopyAction(c, row, "复制历史会话 ID", func() { c.WriteClipboard(i.ID); a.copyFeedback("历史会话 ID") })
						})
					})
					ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(sp2).Children(func() {
						homeText(c, "来源 Agent", homeMeta).Shrink(0)
						ui.Text(c, agentLabel).Font("system-ui").FontSize(fsFootnote).TextColor(tok.Text1).SingleLine()
					})
				})
			})
			a.projectDetailTail(c, s, &i)
		})
	})
}

func (a *App) projectDetailTail(c *ui.Context, s *pane, i *Item) {
	knowledgeBasis(c, *i)
	if i.Source != "" || i.SourceDocument != "" {
		ui.Row(c).FillWidth().Margin(sp4, 0, 0, 0).BorderWidth(1, 0, 0, 0).BorderColor(tok.Border).Padding(sp4, 0, 0, 0).Gap(sp3).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, v4IconDoc).FontSize(16).TextColor(tok.Text3).Shrink(0)
			ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
				ui.Text(c, "出处 · "+readingSourceLabel(*i)).Font("system-ui").FontSize(fsFootnote).FontWeight(550).TextColor(tok.Text1).MaxLines(2)
				sub := "官方关联材料"
				if i.Source != "" {
					sub = sessionIdentity(i.Source)
				}
				homeText(c, sub, homeMeta).SingleLine()
			})
			label := "查看来源会话"
			if i.SourceDocument != "" {
				label = "查看来源文档"
			}
			loadingSource := s.Reading && s.ReadingSource
			if loadingSource {
				label = "正在读取…"
			}
			v4OutlineButton(c, label, nil, func() { a.projectUI.InfoOpen = false; a.source(s) }).Disabled(loadingSource)
		})
	} else if i.Kind == "知识" {
		homeText(c, "官方未提供可定位的会话出处", homeMeta).Margin(sp3, 0, 0, 0)
	}
}
func (a *App) copyFeedback(name string) {
	a.projectUI.Copied = name
	a.projectUI.CopyUntil = time.Now().Add(3 * time.Second)
}
func (a *App) contentInformation(c *ui.Context, anchor ui.Element, i Item) {
	_, h := c.Size()
	fields := itemPresentation(i).TechnicalFields
	if len(fields) == 0 {
		fields = []ReadingField{{"数据组 ID", i.Dataset}, {"对象 ID", i.ID}}
	}
	count := 0
	for _, f := range fields {
		if f.Value != "" {
			count++
		}
	}
	height := min(float32(400), h-160, float32(64+44*count))
	ui.Popover(c, anchor, &a.projectUI.InfoOpen, func() {
		ui.Row(c).Padding(10, 18).Shrink(0).AlignItems(ui.Center).Children(func() {
			homeText(c, "内容信息", homeHeading).FontSize(14)
			ui.Spacer(c)
			if time.Now().Before(a.projectUI.CopyUntil) {
				homeText(c, "已复制", homeMeta)
			}
			readingAction(c, "关闭内容信息", readingClose, func() { a.projectUI.InfoOpen = false })
		})
		ui.Scroll(c.Key("content-info-fields-"+i.Key())).Grow(1).MinHeight(0).TrackScroll(&a.projectUI.InfoScroll).Padding(0, 18, 14, 18).Gap(6).Children(func() {
			for _, field := range fields {
				f := field
				if f.Value == "" {
					continue
				}
				ui.Row(c).MinHeight(38).AlignItems(ui.Center).Gap(12).Children(func() {
					ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
						homeText(c, f.Name, homeMeta).FontSize(10)
						value := f.Value
						if strings.Contains(f.Name, "时间") {
							if t, ok := officialTime(f.Value); ok {
								value = t.In(time.Local).Format("2006-01-02 15:04:05 -07:00")
							}
						} else if r := []rune(value); len(r) > 32 {
							value = string(r[:16]) + "…" + string(r[len(r)-12:])
						}
						row := ui.Row(c).MinWidth(0).AlignItems(ui.Center).Gap(4)
						row.Children(func() {
							homeText(c, value, homeBody).FontSize(11).MinWidth(0).SingleLine().TextColor(homeInk).Tooltip(f.Value)
							hoverCopyAction(c, row, "复制"+f.Name, func() { c.WriteClipboard(f.Value); a.copyFeedback(f.Name) })
						})
					})
				})
			}
		})
	}).Modal().Role(ui.RoleDialog).Width(360).Height(height).Padding(0).Transition(projectOverlayMotion())
}
