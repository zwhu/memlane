package app

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/app/reading"
)

func (a *App) knowledgePage(c *ui.Context, s *pane) {
	if !s.Knowledge.Initialized {
		a.loadKnowledgeFeed(s)
	}
	if s.titleCtx != nil && s.titleCtx.Err() != nil {
		for n := range s.Items {
			if s.Items[n].ReadingState == "pending" {
				s.Items[n].ReadingState = ""
			}
		}
		a.hydrateKnowledgeCards(s)
	}
	if !s.Knowledge.V2Mode {
		a.loadKnowledgeCardRecalls(s)
		if s.Knowledge.RecallOrderPending && s.ListScroll.Y <= 0 {
			a.reorderKnowledgeRange(s, max(knowledgeBatch, len(s.Items)), false)
		}
	}
	ready := true
	if !s.Knowledge.V2Mode {
		for _, i := range s.Items {
			if !i.BodyLoaded && i.Preview == "" && i.ReadingState != "failed" && i.ReadingState != "unavailable" {
				ready = false
			}
		}
	}
	if s.More && s.Loaded && ready && s.ListScroll.MaxY > 0 && s.ListScroll.Y >= s.ListScroll.MaxY-160 {
		a.appendKnowledgeBatch(s)
	}
	if s.Knowledge.V2Mode && s.Knowledge.HasPrev && !s.Knowledge.Prepending && s.Loaded && s.RevealItem == "" && s.ListScroll.MaxY > 0 && s.ListScroll.Y <= 120 {
		a.prependKnowledgeBatch(s)
	}

	readingSplit(c, "project-reading-split", &a.layout.ProjectListWidth, func() {
		ui.Column(c.Key("knowledge-list-pane")).Fill().Background(tok.Bg).Children(func() {
			ui.Row(c.Key("project-view-navigation")).Label("项目列表头部").Height(listHeaderH).Shrink(0).Padding(0, sp4).BorderWidth(0, 0, 1, 0).BorderColor(tok.Border).AlignItems(ui.Center).Children(func() {
				a.projectTabs(c)
				ui.Spacer(c)
			})
			bodyKey := fmt.Sprintf("knowledge-list-body-%d", a.motionRuns.projectPage)
			body := ui.Column(c.Key(bodyKey)).Grow(1).MinHeight(0).FillWidth()
			a.v4PageEnter(c, body, "knowledge-list-body", a.motionRuns.projectPage)
			body.Children(func() {
				a.knowledgeFilterBar(c, s)
				if s.Knowledge.NewCatalogAvailable {
					reading.CatalogUpdateBanner(c, &tok.Tokens, func() {
						homeLink(c, "点击刷新", func() {
							s.Knowledge.NewCatalogAvailable = false
							s.ListScroll.Y = 0
							a.loadKnowledgeFeed(s)
						})
					})
				}
				if s.Knowledge.V2Mode && s.Knowledge.HasPrev && !s.Loading {
					ui.Row(c.Key("knowledge-prev-window-banner")).FillWidth().Padding(sp1, sp4).Gap(sp2).AlignItems(ui.Center).Background(tok.Fill1).Shrink(0).Children(func() {
						if s.Knowledge.Prepending {
							homeText(c, "正在加载上一批记忆…", homeMeta)
						} else {
							homeText(c, "上方仍有更近的记忆", homeMeta)
							ui.Spacer(c)
							homeLink(c, "加载上一批", func() { a.prependKnowledgeBatch(s) })
						}
					})
				}
				if s.Error != "" {
					ui.Row(c).Padding(sp1, sp4).Gap(sp2).AlignItems(ui.Center).Shrink(0).Children(func() {
						homeText(c, "读取未完成 · "+s.Error, homeMeta).MaxLines(2)
						projectLink(c, "重试读取记忆", func() { a.loadKnowledgeFeed(s) })
					})
				}
				a.knowledgeList(c, s)
			})
			totalFiltered := s.Knowledge.TotalFiltered
			if !s.Knowledge.V2Mode {
				totalFiltered = len(a.filteredKnowledgeCatalog(s))
			}
			appFooter(c, "记忆加载状态").Padding(0, sp4).Children(func() {
				if s.Loaded || len(s.Items) > 0 {
					homeText(c, fmt.Sprintf("已载入 %d 项", len(s.Items)), homeMeta)
				}
				ui.Spacer(c)
				switch {
				case !s.Loaded:
				case s.More:
					homeText(c, fmt.Sprintf("共 %d 条 · 向下滚动加载", totalFiltered), homeMeta)
				case totalFiltered > 0:
					homeText(c, fmt.Sprintf("共 %d 条", totalFiltered), homeMeta)
				}
			})
		})
	}, func() {
		a.projectDetail(c, s)
	})
}

func (a *App) knowledgeList(c *ui.Context, s *pane) {
	if !s.Loaded && s.Loading && len(s.Items) == 0 {
		ui.Column(c).Grow(1).MinHeight(0).Padding(sp2, sp4).Gap(sp3).Label("正在读取记忆…").Children(func() {
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
		label := "此项目暂无记忆"
		switch {
		case len(s.Knowledge.RecallFilter) > 0:
			label = "本次召回的记忆暂不可用"
		case s.Knowledge.CategoryFilter != "" || s.Knowledge.UnreadOnly:
			label = "没有符合筛选条件的记忆"
		}
		v4EmptyState(c, "knowledge-empty", label, "")
		return
	}
	s.ListSelection = -1
	listSelected := s.Selected
	if sourceSession(s) && len(s.History) > 0 {
		listSelected = &s.History[len(s.History)-1]
	}
	for n, item := range s.Items {
		if listSelected != nil && listSelected.Key() == item.Key() {
			s.ListSelection = n
			break
		}
	}
	s.WidgetList.Key = func(n int) any { return s.Items[n].Key() }
	s.WidgetList.KeepOffset = true
	s.WidgetList.Label = func(n int) string {
		if body := knowledgeCardBody(s.Items[n]); body != "" {
			return body
		}
		if preview := strings.TrimSpace(s.Items[n].Preview); preview != "" {
			return preview
		}
		return s.Items[n].Title
	}
	s.WidgetList.Selected = &s.ListSelection
	if s.Selected != nil && s.RevealItem == s.Selected.Key() && s.ListSelection >= 0 {
		s.WidgetList.ScrollTo(s.ListSelection, ui.Center)
		s.RevealItem = ""
	}
	ui.Themed(c, listTheme).Grow(1).MinHeight(0).FillWidth().Background(ui.Transparent).Children(func() {
		ui.List(c.Key("knowledge-list-"+a.routeKey()), &s.WidgetList, len(s.Items), func(n int) {
			i := s.Items[n]
			a.knowledgeListItem(c, s, i, n, n == s.ListSelection)
		}).Label("记忆列表").Grow(1).MinHeight(0).TrackScroll(&s.ListScroll).Padding(sp1, sp2, sp3, sp2).Gap(2).OnChange(func() {
			if s.ListSelection < 0 || s.ListSelection >= len(s.Items) {
				return
			}
			a.projectUI.InfoOpen = false
			s.History = nil
			a.selectItem(s, s.Items[s.ListSelection])
		})
	})
}

func (a *App) knowledgeListItem(c *ui.Context, s *pane, i Item, index int, selected bool) {
	itemKey := fmt.Sprintf("kitem-%d-%s", a.motionRuns.projectList, i.Key())
	col := ui.Column(c.Key(itemKey)).FillWidth().MinWidth(0).Padding(10, sp3).Gap(6)
	a.v4StaggerEnter(c, col, "kitem-"+i.Key(), index, a.motionRuns.projectList, false)
	col.Children(func() {
		body := knowledgeCardBody(i)
		switch {
		case i.ReadingState == "failed" || i.ReadingState == "unavailable":
			homeText(c, "记忆暂不可读", homeMeta)
			projectLink(c, "重试读取正文", func() {
				for n, row := range s.Items {
					if row.Key() == i.Key() {
						s.Items[n].ReadingState = ""
					}
				}
				a.hydrateKnowledgeCards(s)
			})
		case !i.BodyLoaded:
			preview := strings.TrimSpace(i.Preview)
			if preview != "" {
				v4InlineMarkdown(c, preview, fsFootnote, 400, 1.55, tok.Text1).MaxLines(knowledgePreviewLines)
			} else {
				fallbackTitle := strings.TrimSpace(i.Title)
				ui.Column(c).FillWidth().MinHeight(54).Children(func() {
					if fallbackTitle == "" {
						ui.Column(c).FillWidth().Gap(6).Children(func() {
							ui.Box(c).FillWidth().Height(11).Radius(radiusSmall).Background(tok.Fill2)
							ui.Box(c).Width(180).Height(11).Radius(radiusSmall).Background(tok.Fill1)
						})
					} else {
						v4InlineMarkdown(c, fallbackTitle, fsFootnote, 400, 1.55, tok.Text2).MaxLines(knowledgePreviewLines)
					}
				})
			}
		default:
			v4InlineMarkdown(c, body, fsFootnote, 400, 1.55, tok.Text1).MaxLines(knowledgePreviewLines)
		}
		ui.Row(c).FillWidth().MinWidth(0).AlignItems(ui.Center).Gap(sp2).Children(func() {
			if a.itemIsUnread(i) {
				v4UnreadDot(c)
			}
			agentLogo(c, recentAgentIcon(i), 16).Tooltip("来源 Agent：" + sourceAgentName(i))
			v4RecallChip(c, i)
			if fields := knowledgeBasisFields(i); len(fields) > 0 {
				v4CategoryChip(c, fields[0].Value)
			}
			ui.Spacer(c)
			ui.Text(c, knowledgeDateLabel(i)).Font("system-ui").FontSize(fsCaption).FontFeatures("tnum").TextColor(tok.Text3).SingleLine().Shrink(0)
		})
	})
}

// knowledgeDialog is a no-op under V4: project knowledge detail lives inline
// in the right pane of the three-pane view.
func (a *App) knowledgeDialog(c *ui.Context) {}

func (a *App) closeKnowledgeDetail(s *pane) {
	s.Knowledge.Detail = false
	a.projectUI.InfoOpen = false
	s.History = nil
	if s.HomeAnchor != nil {
		s.HomeAnchor = nil
		a.loadKnowledgeFeed(s)
	}
}
