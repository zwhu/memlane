package app

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/app/reading"
	"github.com/zwhu/memlane/app/settings"
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
		options := settings.ProjectLabels(a.Config.Common)
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
		if s.Selected != nil && s.Selected.Key() == item.Key() {
			s.ListSelection = n
			break
		}
	}
	s.WidgetList.Key = func(n int) any { return s.Items[n].Key() }
	s.WidgetList.KeepOffset = true
	s.WidgetList.Label = func(n int) string {
		if title := strings.TrimSpace(s.Items[n].Title); title != "" {
			return title
		}
		return commonListPreview(s.Items[n])
	}
	s.WidgetList.Selected = &s.ListSelection
	if s.Selected != nil && s.RevealItem == s.Selected.Key() && s.ListSelection >= 0 {
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
	col := ui.Column(c.Key("citem-"+i.Key())).FillWidth().MinWidth(0).Padding(10, sp3).Gap(6)
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
