package simpledesktop

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"github.com/zwhu/memlane/app/reading"
)

type commonKnowledgePageState struct {
	Options, Discard, MenuOpen bool
	EditorScroll               ui.ScrollState
	AccessScroll               ui.ScrollState
}

var commonPlus = homeSVG(`<path d="M12 5v14M5 12h14"/>`)
var commonEditIcon = homeSVG(`<path d="m16 3 5 5-12 12H4v-5zM13 6l5 5"/>`)

var commonCategoryLabel = reading.CommonCategoryLabel

func commonHasDraft(e commonEditorState) bool {
	return e.HasDraft()
}

func commonButton(c *ui.Context, label string, primary bool, fn func()) ui.Element {
	if primary {
		return ui.PrimaryButton(c, label).OnClick(fn)
	}
	return textButton(c, label, fn)
}
func (a *App) beginCommonDraft(item *Item) {
	e := &a.commonEdit
	a.projectUI.InfoOpen = false
	if e.DeleteSubmitted {
		e.Message = "先核对原删除结果，再开始新的修改。"
		return
	}
	if commonHasDraft(*e) || e.Busy {
		e.Open = true
		return
	}
	p, ok := a.current()
	if !ok || p.Dataset == "" || a.Config.ProjectControl == nil {
		return
	}
	a.commonGeneration++
	category := e.Category
	next := commonEditorState{Generation: a.commonGeneration, Open: true, Target: p, Category: category, Draft: CommonDraft{Category: "preference"}}
	if a.commonCategoryFilter() != "" {
		next.Draft.Category = a.commonCategoryFilter()
	}
	if item != nil {
		if item.Document == nil || !item.BodyLoaded || item.Dataset != p.Dataset || commonCategory(*item.Document) == "" {
			return
		}
		d := *item.Document
		next.Existing = &d
		next.Draft = commonDraftFor(*item)
		next.Baseline = reading.BaselineSHA256(item.Body)
	}
	a.commonEdit = next
	a.commonPage = commonKnowledgePageState{Options: next.Draft.Source != "" || next.Draft.Applicability != ""}
}
func (a *App) discardCommonDraft() {
	e := &a.commonEdit
	if !e.CanDiscard() {
		return
	}
	category := e.Category
	a.commonGeneration++
	a.commonEdit = commonEditorState{Generation: a.commonGeneration, Category: category}
	a.commonPage = commonKnowledgePageState{}
}
func (a *App) commonListHeader(c *ui.Context) {
	s := a.state()
	e := &a.commonEdit
	ui.Row(c).Label("通用记忆列表头部").Height(listHeaderH).Shrink(0).Padding(0, sp3, 0, sp4).
		BorderWidth(0, 0, 1, 0).BorderColor(tok.Border).AlignItems(ui.Center).Gap(sp2).Children(func() {
		ui.Text(c, "记忆库").Font("system-ui").FontSize(fsBody).FontWeight(550).TextColor(tok.Text1).SingleLine()
		ui.Spacer(c)
		label := "新增通用记忆"
		icon := v4IconPlus
		if commonHasDraft(*e) {
			label = "继续草稿"
			icon = nil
			if e.Submitted {
				label = "查看保存结果"
			}
		}
		b := v4SmallButton(c, label, icon, func() { a.beginCommonDraft(nil) })
		p, ok := a.current()
		b.Disabled(a.Config.ProjectControl == nil || !ok || p.Dataset == "" || e.DeleteSubmitted)
		refreshAction(c, "刷新通用记忆", func() {
			s.CommonPageIndex = 0
			s.CommonNav = nil
			s.CommonCursors = nil
			s.CommonNextCursor = ""
			s.CommonPrevCursor = ""
			s.CommonHasPrev = false
			s.Offset = 0
			a.load(s)
		})
	})
}

func (a *App) commonFilterBar(c *ui.Context) {
	s := a.state()
	e := &a.commonEdit
	labels := []string{"全部", "个人偏好", "通用经验"}
	selected := 0
	for n, label := range labels {
		if e.Category == label {
			selected = n
		}
	}
	v4Segmented(c, "common-category-seg", "通用记忆分类", &selected, labels, func() {
		e.Category = ""
		if selected > 0 {
			e.Category = labels[selected]
		}
		e.Open = false
		s.Offset = 0
		s.CommonPageIndex = 0
		s.CommonNav = nil
		s.CommonCursors = nil
		s.CommonNextCursor = ""
		s.CommonPrevCursor = ""
		s.CommonHasPrev = false
		a.load(s)
	}).FillWidth()
	if len(a.Config.Common) > 1 {
		options := projectLabels(a.Config.Common)
		selected := options[min(max(a.project, 0), len(options)-1)]
		selectOpts := make([]v4SelectOption, 0, len(options))
		for _, opt := range options {
			selectOpts = append(selectOpts, v4SelectOption{Value: opt, Label: opt})
		}
		v4Select(c, "common-library-select", "通用记忆库", selected, selectOpts,
			e.Open || e.Busy || (e.Submitted && !e.Completed) || e.DeleteSubmitted, false,
			func(next string) {
				for n, label := range options {
					if label == next {
						a.navigate("通用知识", n)
						break
					}
				}
			}).FillWidth()
	}
}

func (a *App) commonKnowledgePage(c *ui.Context) {
	s := a.state()
	e := &a.commonEdit
	readingSplit(c, "common-reading-split", &a.layout.CommonListWidth, func() {
		ui.Column(c.Key("common-list-pane")).Fill().Background(tok.Bg).Children(func() {
			a.commonListHeader(c)
			ui.Column(c).Shrink(0).Padding(sp3, sp4, sp2, sp4).Gap(sp2).Children(func() {
				a.commonFilterBar(c)
				if len(s.Knowledge.RecallFilter) > 0 {
					ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(sp2).Padding(sp1, 0, 0, 0).Children(func() {
						ui.Text(c, fmt.Sprintf("本次召回 · %d 条", len(s.Knowledge.RecallFilter))).Font("system-ui").FontSize(fsCaption).FontWeight(500).TextColor(tok.Accent).SingleLine()
						ui.Spacer(c)
						v4SmallButton(c, "显示全部记忆", nil, func() {
							s.Knowledge.RecallFilter = nil
							s.Knowledge.RecallAgentID, s.Knowledge.RecallEventID = "", ""
							s.Offset, s.CommonPageIndex = 0, 0
							s.CommonNav, s.CommonCursors = nil, nil
							a.load(s)
						})
						v4SmallButton(c, "返回动态", nil, func() { a.navigate("总览", 0) })
					})
				}
				if s.Error != "" {
					message := "读取失败 · " + s.Error
					if s.Loaded {
						message = "未能刷新，显示上次结果 · " + s.Error
					}
					ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(sp2).Children(func() {
						ui.Text(c, message).Font("system-ui").FontSize(fsCaption).TextColor(tok.Danger).Grow(1).MinWidth(0).MaxLines(2)
						v4SmallButton(c, "重试读取", nil, func() { a.load(s) })
					})
				}
				if !e.Open && e.Message != "" {
					homeText(c, e.Message, homeMeta).MaxLines(3)
				}
				if e.DeleteSubmitted {
					v4SmallButton(c, "核对原删除结果", nil, a.confirmCommonDeletion).Disabled(e.Busy)
				}
			})
			a.commonKnowledgeList(c, s)
			appFooter(c, "通用记忆分页栏").Padding(0, sp4).Gap(sp2).Children(func() {
				homeText(c, projectListStatus(s), homeMeta)
				ui.Spacer(c)
				pageNum := s.Offset/30 + 1
				prevDisabled := s.Offset == 0 || s.Loading
				if s.CommonV2Mode {
					pageNum = s.CommonPageIndex + 1
					prevDisabled = (s.CommonPageIndex == 0 && !s.CommonHasPrev) || s.Loading
				}
				v4SmallButton(c, "上一页", nil, func() {
					if s.CommonV2Mode {
						a.stepCommonKnowledgeV2(s, -1)
					} else {
						s.Offset = max(0, s.Offset-30)
						a.load(s)
					}
				}).Disabled(prevDisabled)
				ui.Text(c, fmt.Sprint(pageNum)).Font("system-ui").FontSize(fsCaption).FontFeatures("tnum").TextColor(tok.Text3).SingleLine()
				v4SmallButton(c, "下一页", nil, func() {
					if s.CommonV2Mode {
						a.stepCommonKnowledgeV2(s, 1)
					} else {
						s.Offset += 30
						a.load(s)
					}
				}).Disabled(!s.More || s.Loading || (s.CommonV2Mode && s.CommonNextCursor == ""))
			})
		})
	}, func() {
		if e.Open {
			a.commonKnowledgeEditor(c)
		} else {
			a.commonKnowledgeReader(c, s)
		}
	})
}

func commonListPreview(i Item) string {
	if i.BodyLoaded {
		if parts, canonical := commonReadingParts(i); canonical {
			body := strings.TrimSpace(strings.ReplaceAll(parts.Body, "\n", " "))
			title := strings.TrimSpace(parts.Title)
			if title != "" && body != "" {
				return "**" + strings.ReplaceAll(title, "**", "") + "**  " + body
			}
			if body != "" {
				return body
			}
			if title != "" {
				return title
			}
		}
		if body := knowledgeCardBody(i); body != "" {
			return strings.ReplaceAll(body, "\n", " ")
		}
	}
	if preview := strings.TrimSpace(i.Preview); preview != "" {
		return strings.ReplaceAll(preview, "\n", " ")
	}
	return strings.TrimSpace(i.Title)
}

func (a *App) commonKnowledgeList(c *ui.Context, s *pane) {
	if !s.Loaded && s.Loading && len(s.Items) == 0 {
		ui.Column(c).Grow(1).MinHeight(0).Padding(sp2, sp4).Gap(sp3).Label("正在读取通用记忆…").Children(func() {
			for n := 0; n < 5; n++ {
				ui.Column(c).FillWidth().Padding(10, sp2).Gap(6).Children(func() {
					ui.Box(c).FillWidth().Height(11).Radius(radiusSmall).Background(tok.Fill2)
					ui.Box(c).Width(240).Height(11).Radius(radiusSmall).Background(tok.Fill2)
					ui.Box(c).Width(140).Height(9).Radius(3).Background(tok.Fill1)
				})
			}
		})
		return
	}
	if s.Loaded && s.Error == "" && len(s.Items) == 0 {
		label := "还没有通用记忆"
		hint := "可新增个人偏好或可复用的经验。"
		if a.commonCategoryFilter() != "" || a.commonEdit.Category != "" {
			label = "当前分类没有通用记忆"
			hint = ""
		}
		v4EmptyState(c, "common-list-empty", label, hint)
		return
	}
	s.ListSelection = -1
	for n, item := range s.Items {
		if s.Selected != nil && s.Selected.key() == item.key() {
			s.ListSelection = n
			break
		}
	}
	s.WidgetList.Key = func(n int) any { return s.Items[n].key() }
	s.WidgetList.KeepOffset = true
	s.WidgetList.Label = func(n int) string {
		if title := strings.TrimSpace(s.Items[n].Title); title != "" {
			return title
		}
		return commonListPreview(s.Items[n])
	}
	s.WidgetList.Selected = &s.ListSelection
	if s.Selected != nil && s.RevealItem == s.Selected.key() && s.ListSelection >= 0 {
		s.WidgetList.ScrollTo(s.ListSelection, ui.Center)
		s.RevealItem = ""
	}
	ui.Themed(c, listTheme).Grow(1).MinHeight(0).FillWidth().Background(ui.Transparent).Children(func() {
		ui.List(c.Key("common-list-"+a.routeKey()), &s.WidgetList, len(s.Items), func(n int) {
			i := s.Items[n]
			a.commonListItem(c, s, i, n, n == s.ListSelection)
		}).Label("通用记忆列表").Grow(1).MinHeight(0).TrackScroll(&s.ListScroll).Padding(sp1, sp2, sp3, sp2).Gap(2).OnChange(func() {
			if s.ListSelection < 0 || s.ListSelection >= len(s.Items) {
				return
			}
			a.commonEdit.Open = false
			a.projectUI.InfoOpen = false
			a.selectItem(s, s.Items[s.ListSelection])
		})
	})
}

func (a *App) commonListItem(c *ui.Context, s *pane, i Item, index int, selected bool) {
	col := ui.Column(c.Key("citem-"+i.key())).FillWidth().MinWidth(0).Padding(10, sp3).Gap(6)
	if index > 0 && !selected && index-1 != s.ListSelection {
		col.BorderWidth(1, 0, 0, 0).BorderColor(tok.Border)
	}
	col.Children(func() {
		preview := commonListPreview(i)
		switch {
		case i.ReadingState == "failed":
			homeText(c, "标题读取失败", homeMeta)
			v4SmallButton(c, "重试读取", nil, func() { a.retryReadingTitle(s, i) })
		case preview == "":
			ui.Column(c).FillWidth().MinHeight(38).Gap(6).Justify(ui.Center).Children(func() {
				ui.Box(c).FillWidth().Height(11).Radius(radiusSmall).Background(tok.Fill2)
				ui.Box(c).Width(180).Height(11).Radius(radiusSmall).Background(tok.Fill1)
			})
		default:
			colText := tok.Text1
			if !i.BodyLoaded && strings.TrimSpace(i.Preview) == "" {
				colText = tok.Text2
			}
			v4InlineMarkdown(c, preview, fsFootnote, 400, 1.55, colText).MaxLines(knowledgePreviewLines)
		}
		ui.Row(c).FillWidth().MinWidth(0).AlignItems(ui.Center).Gap(sp2).Children(func() {
			agentLogo(c, recentAgentIcon(i), 16).Tooltip("来源 Agent：" + sourceAgentName(i))
			v4CategoryChip(c, commonCategoryLabel(i.CommonCategory))
			v4RecallChip(c, i)
			ui.Spacer(c)
			ui.Text(c, displayDate(i.Date)).Font("system-ui").FontSize(fsCaption).FontFeatures("tnum").TextColor(tok.Text3).SingleLine().Shrink(0)
		})
	})
}

func commonEmptyWithAction(c *ui.Context, key, message, hint string, action func()) {
	ui.Column(c).Grow(1).MinHeight(0).FillWidth().AlignItems(ui.Center).Justify(ui.Center).Gap(sp3).Padding(sp6).Children(func() {
		ui.Column(c).FillWidth().MaxHeight(200).AlignItems(ui.Center).Children(func() {
			v4EmptyState(c, key, message, hint)
		})
		if action != nil {
			action()
		}
	})
}

func (a *App) commonReadActions(c *ui.Context, i Item) {
	if a.Config.ProjectControl == nil || i.Document == nil || commonCategory(*i.Document) == "" || !i.BodyLoaded {
		return
	}
	e := &a.commonEdit
	pending := e.Submitted && !e.Completed
	label := "编辑"
	if commonHasDraft(*e) {
		label = "继续草稿"
		if pending {
			label = "查看保存结果"
		}
	}
	v4SmallButton(c, label, nil, func() { a.beginCommonDraft(&i) }).Disabled(e.Busy || e.DeleteSubmitted)
	v4DangerOutlineButton(c, "删除", true, func() { d := *i.Document; e.Delete = &d; e.Message = "" }).Disabled(e.Busy || pending || e.DeleteSubmitted)
}
func commonReadingParts(i Item) (CommonDraft, bool) {
	if i.Document == nil {
		return CommonDraft{}, false
	}
	d := commonDraftFor(i)
	raw, err := d.FormatDocument()
	return d, err == nil && strings.TrimSuffix(raw, "\n") == strings.TrimSuffix(i.Body, "\n")
}
func (a *App) commonKnowledgeReader(c *ui.Context, s *pane) {
	e := &a.commonEdit
	ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Background(tok.Bg).Children(func() {
		if s.Selected == nil {
			switch {
			case len(a.Config.Common) == 0:
				commonEmptyWithAction(c, "common-unconnected", "尚未连接记忆库", "", func() {
					v4OutlineButton(c, "前往设置", nil, func() { a.navigate("设置", 0) })
				})
			case !s.Loaded:
				if s.Error != "" {
					v4EmptyState(c, "common-loading", "读取失败", "")
				} else {
					ui.Column(c).Grow(1).MinHeight(0).FillWidth()
				}
			case len(s.Items) > 0:
				v4EmptyState(c, "common-select", "选择一条记忆", "")
			case s.Error != "":
				v4EmptyState(c, "common-refresh", "未能刷新，显示上次结果", "")
			case e.Category != "" || s.Offset > 0:
				commonEmptyWithAction(c, "common-filter-empty", "当前分类没有记忆", "", func() {
					v4OutlineButton(c, "显示全部", nil, func() { e.Category = ""; s.Offset = 0; a.load(s) })
				})
			default:
				commonEmptyWithAction(c, "common-empty", "还没有通用记忆", "个人偏好和跨项目经验放在这里。", func() {
					if a.Config.ProjectControl != nil {
						v4PrimaryButton(c, "添加记忆", func() { a.beginCommonDraft(nil) })
					}
				})
			}
			appFooter(c, "通用记忆阅读栏").Padding(0, sp6).Children(func() { homeText(c, "跨项目参考 · 项目明确约定优先", homeMeta) })
			return
		}
		i := *s.Selected
		if e.Delete != nil && e.Delete.ID == i.ID && e.Delete.Dataset == i.Dataset {
			a.commonDeletionBanner(c, i)
		}
		scroll := s.DetailScroll[i.key()]
		if scroll == nil {
			scroll = &ui.ScrollState{}
			s.DetailScroll[i.key()] = scroll
		}
		ui.Scroll(c.Key("common-detail-"+i.key())).Grow(1).MinHeight(0).TrackScroll(scroll).Padding(sp6, sp10, sp8, sp10).Children(func() {
			ui.Column(c).FillWidth().MaxWidth(readerMaxW).Gap(sp4).Children(func() {
				ui.Row(c).Label("通用记忆内容工具栏").FillWidth().MinHeight(28).Padding(0, 0, sp4, 0).Gap(sp2).AlignItems(ui.Center).
					BorderWidth(0, 0, 1, 0).BorderColor(tok.Border).Children(func() {
					agentLogo(c, recentAgentIcon(i), 18)
					v4CategoryChip(c, commonCategoryLabel(i.CommonCategory))
					row := ui.Row(c).MinWidth(0).AlignItems(ui.Center).Gap(4)
					row.Children(func() {
						ui.Text(c, displayDate(i.Date)).Font("system-ui").FontSize(fsCaption).FontFeatures("tnum").TextColor(tok.Text3).SingleLine()
						hoverCopyAction(c, row, "复制内容", func() { c.WriteClipboard(itemPresentation(i).VisibleBody); a.copyFeedback("内容") }).Disabled(!i.BodyLoaded)
					})
					v4RecallChip(c, i)
					knowledgeBasisTag(c, i)
					ui.Spacer(c)
					anchor := readingAction(c, "内容信息", readingInfo, func() { a.projectUI.InfoOpen = !a.projectUI.InfoOpen; a.projectUI.InfoKey = i.key() })
					if a.projectUI.InfoKey != i.key() {
						a.projectUI.InfoOpen = false
					}
					a.contentInformation(c, anchor, i)
					a.commonReadActions(c, i)
				})
				parts, canonical := commonReadingParts(i)
				title := projectDisplayTitle(i)
				if canonical {
					title = parts.Title
				}
				homeText(c, title, homeHeading).FontSize(fsTitle).LineHeight(1.35)
				if s.Error != "" {
					homeText(c, "内容未刷新 · "+s.Error, homeMeta)
				}
				if !i.BodyLoaded && s.Reading {
					v4SkeletonGroup(c, 3, 2, false, false)
				} else if canonical {
					a.commonMarkdown(c, "common-body-"+i.key(), parts.Body)
					if parts.Applicability != "" {
						ui.Column(c).FillWidth().Margin(sp2, 0, 0, 0).Padding(sp4, 0, 0, 0).Gap(sp2).
							BorderWidth(1, 0, 0, 0).BorderColor(tok.Border).Children(func() {
							ui.Text(c, "适用条件").Font("system-ui").FontSize(fsCaption).FontWeight(600).TextColor(tok.Text3).SingleLine()
							a.commonMarkdown(c, "common-conditions-"+i.key(), parts.Applicability)
						})
					}
					if parts.Source != "" {
						ui.Column(c).FillWidth().Margin(sp2, 0, 0, 0).Padding(sp4, 0, 0, 0).Gap(sp2).
							BorderWidth(1, 0, 0, 0).BorderColor(tok.Border).Children(func() {
							ui.Text(c, "原始来源").Font("system-ui").FontSize(fsCaption).FontWeight(600).TextColor(tok.Text3).SingleLine()
							ui.Text(c, parts.Source).Font("system-ui").FontSize(fsFootnote).FontWeight(400).TextColor(tok.Text2).LineHeight(1.6)
						})
					}
				} else {
					a.commonMarkdown(c, "common-original-"+i.key(), itemPresentation(i).VisibleBody)
				}
				if i.BodyTruncated {
					ui.Text(c, "正文超过 8 MiB，已截断显示").FontSize(fsCaption).TextColor(tok.Text3)
				}
			})
		})
		appFooter(c, "通用记忆阅读栏").Padding(0, sp6).Gap(sp2).Children(func() {
			homeText(c, "跨项目参考 · 项目明确约定优先", homeMeta)
			ui.Spacer(c)
			if time.Now().Before(a.projectUI.CopyUntil) {
				homeText(c, a.projectUI.Copied+"已复制", homeMeta)
			}
			v4OutlineButton(c, "复制正文", v4IconCopy, func() {
				c.WriteClipboard(itemPresentation(i).VisibleBody)
				a.copyFeedback("正文")
			}).Disabled(!i.BodyLoaded)
		})
	})
}
func (a *App) commonMarkdown(c *ui.Context, key, body string) {
	markdown.View(c.Key(key), body, a.v4MarkdownOptions(fsReading))
}
func (a *App) commonDeletionBanner(c *ui.Context, i Item) {
	e := &a.commonEdit
	if !e.DeleteSubmitted {
		asking := true
		reading.ConfirmDelete(c, &tok.Tokens, &asking, i.ID, e.Busy,
			func() { e.Delete = nil }, func() { a.deleteCommonKnowledge(*i.Document) })
		return
	}

	ui.Row(c).Shrink(0).Padding(sp2, sp10).Gap(sp3).AlignItems(ui.Center).
		BorderWidth(0, 0, 1, 0).BorderColor(tok.Danger.Alpha(.35)).Background(tok.Danger.Alpha(.08)).Children(func() {
		v4StatusDot(c, "fail")
		ui.Text(c, "删除结果尚未确认，请核对原操作，不重复删除。").Font("system-ui").FontSize(fsCaption).TextColor(tok.Text1).Grow(1).MinWidth(0).MaxLines(2)
		v4SmallButton(c, "核对原删除结果", nil, a.confirmCommonDeletion).Disabled(e.Busy)
	})
}
func (a *App) confirmCommonDeletion() {
	e := &a.commonEdit
	if e.Busy || !e.DeleteSubmitted || e.Delete == nil {
		return
	}
	e.Busy = true
	d, p, op := *e.Delete, e.DeleteTarget, e.Generation
	control, ok := a.accessClient()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		var present bool
		var err error
		if ok {
			var controller reading.CommonController
			controller, err = commonControllerFor(control)
			if err == nil {
				_, err = controller.Get(ctx, d.ID)
			}
			if legacyReadingRouteMissing(err) {
				_, err = control.documentLookupV2(ctx, p.Dataset, d.ID)
			}
			present = err == nil
			if reading.Status(err, 404) || isControlErrorCode(err, http.StatusNotFound, "document_not_found") {
				err = nil
			}
		} else {
			err = fmt.Errorf("控制面服务不可用，无法核对官方删除结果")
		}
		a.Update(func() {
			if op != a.commonEdit.Generation {
				return
			}
			e.Busy = false
			if err != nil {
				e.Message = "删除结果仍未确认：" + err.Error()
				return
			}
			if present {
				e.DeleteSubmitted = false
				e.Message = "官方仍保留此条记忆；可重新确认删除，未自动重发。"
				return
			}
			e.DeleteSubmitted = false
			e.Delete = nil
			e.Message = "已核对：官方记忆已移除"
			a.unavailableReadingItem(Item{ID: d.ID, Dataset: p.Dataset, Kind: "知识", Document: &d})
			if cur, ok := a.current(); ok && a.page == "通用知识" && cur.Dataset == p.Dataset {
				a.load(a.state())
			}
		})
	}()
}
func (a *App) commonKnowledgeEditor(c *ui.Context) {
	e := &a.commonEdit
	_, h := c.Size()
	locked := e.Busy || e.Submitted
	ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Background(tok.Bg).Children(func() {
		ui.Row(c).Label("通用记忆编辑工具栏").Height(listHeaderH).Shrink(0).Padding(0, sp10).AlignItems(ui.Center).
			BorderWidth(0, 0, 1, 0).BorderColor(tok.Border).Children(func() {
			ui.Row(c).FillWidth().MaxWidth(readerMaxW).AlignItems(ui.Center).Gap(sp3).Children(func() {
				title := "新增通用记忆"
				if e.Existing != nil {
					title = "编辑通用记忆"
				}
				if e.Submitted {
					title = "保存结果"
				}
				ui.Text(c, title).Font("system-ui").FontSize(fsBody).FontWeight(550).TextColor(tok.Text1).SingleLine()
				ui.Spacer(c)
				catIdx := 0
				if e.Draft.Category == "experience" {
					catIdx = 1
				}
				catDisabled := locked || e.Existing != nil
				seg := v4Segmented(c, "common-edit-category", "记忆分类", &catIdx, []string{"个人偏好", "通用经验"}, func() {
					if catDisabled {
						return
					}
					if catIdx == 0 {
						e.Draft.Category = "preference"
					} else {
						e.Draft.Category = "experience"
					}
				})
				if catDisabled {
					seg.Disabled(true).Opacity(0.55).Tooltip("编辑已有记忆时不可切换分类")
				}
			})
		})
		ui.Scroll(c.Key("common-compact-editor")).Grow(1).MinHeight(0).TrackScroll(&a.commonPage.EditorScroll).Padding(sp6, sp10, sp8, sp10).Children(func() {
			ui.Column(c).FillWidth().MaxWidth(readerMaxW).Gap(sp5).Children(func() {
				if len(a.Config.Common) > 1 {
					homeText(c, "保存到："+e.Target.Name, homeMeta)
				}
				ui.Field(c, "标题", func() {
					ui.TextInput(c.Key("common-draft-title-"+fmt.Sprint(e.Generation)), &e.Draft.Title).Label("通用记忆标题").Placeholder("一句话概括这条记忆").FillWidth().Height(36).FontSize(fsBody).Disabled(locked)
				})
				ui.Field(c, "正文", func() {
					ui.TextArea(c.Key("common-draft-body-"+fmt.Sprint(e.Generation)), &e.Draft.Body).Label("通用记忆正文").Placeholder("写下可复用的约定或经验，支持 Markdown").FillWidth().Height(min(float32(260), max(float32(180), (h-380)/2))).FontSize(fsReading).Disabled(locked)
				})
				ui.Column(c).FillWidth().Padding(sp4, 0, 0, 0).Gap(sp4).BorderWidth(1, 0, 0, 0).BorderColor(tok.Border).Children(func() {
					label := "补充适用条件与来源（可选）"
					if a.commonPage.Options {
						label = "收起补充信息"
					}
					v4SmallButton(c, label, nil, func() { a.commonPage.Options = !a.commonPage.Options })
					if a.commonPage.Options {
						ui.Field(c, "适用条件", func() {
							ui.TextArea(c.Key("common-draft-applicability-"+fmt.Sprint(e.Generation)), &e.Draft.Applicability).Label("通用记忆适用条件").Placeholder("例如：适用于 TypeScript 项目").FillWidth().Height(72).FontSize(fsFootnote).Disabled(locked)
						})
						ui.Field(c, "原始来源", func() {
							ui.TextInput(c.Key("common-draft-source-"+fmt.Sprint(e.Generation)), &e.Draft.Source).Label("通用记忆来源").Placeholder("可填写出处、链接或说明").FillWidth().Height(34).FontSize(fsFootnote).Disabled(locked || e.Existing != nil)
						})
						if e.Existing != nil {
							homeText(c, "编辑保留创建时的分类与原始来源。", homeMeta)
						}
					}
				})
				if e.Submitted {
					ui.Column(c).FillWidth().Padding(sp3, sp4).Gap(sp2).BorderWidth(1, 0, 0, 0).BorderColor(tok.Border).Background(tok.Fill1).Radius(radius).Children(func() {
						saved := "待核对"
						if e.SaveVerified {
							saved = "已核对"
						}
						processing := e.Processing
						if processing == "" {
							processing = "待核对"
						}
						homeText(c, "正文保存："+saved, homeBody).FontSize(fsFootnote)
						homeText(c, "提炼状态："+processing, homeBody).FontSize(fsFootnote)
						if e.Receipt.PipelineRunID != "" {
							homeText(c, "原提炼记录："+e.Receipt.PipelineRunID, homeMeta).MaxLines(2)
						}
					})
				}
				if e.Message != "" {
					homeText(c, e.Message, homeBody).FontSize(fsFootnote).LineHeight(1.7)
				}
			})
		})
		appFooter(c, "通用记忆编辑栏").Padding(0, sp6).Gap(sp2).Children(func() {
			status := "仅保存在当前编辑草稿"
			if e.Busy {
				status = "正在核对官方操作…"
			} else if e.Submitted {
				status = "本次已提交，不重复发送"
			}
			homeText(c, status, homeMeta).Grow(1).MinWidth(0).MaxLines(2)
			if !e.Submitted {
				v4SmallButton(c, "放弃草稿", nil, func() { a.commonPage.Discard = true }).Disabled(e.Busy)
			}
			v4OutlineButton(c, "返回阅读", nil, func() { e.Open = false }).Disabled(e.Busy)
			if e.Submitted {
				ui.PrimaryButton(c, "核对保存结果").OnClick(a.confirmCommonKnowledge).Disabled(e.Busy)
				if e.SaveVerified && (e.Processing == "已完成" || strings.HasPrefix(e.Processing, "失败")) {
					v4OutlineButton(c, "完成", nil, func() { e.Completed = true; e.Open = false })
				}
			} else {
				_, err := e.Draft.FormatDocument()
				ui.PrimaryButton(c, "保存").OnClick(a.saveCommonKnowledge).Disabled(e.Busy || err != nil)
			}
		})
	})
	reading.ConfirmDiscard(c, &tok.Tokens, &a.commonPage.Discard, func() {
		a.commonPage.Discard = false
		a.discardCommonDraft()
	})
}
