package simpledesktop

import (
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

func (a *App) openSearchResultsPage() {
	s := &a.searchUI
	origin := a.readingLocation()
	if a.searchOrigin != nil {
		origin = a.searchOrigin
	}
	// Snapshot search results state before navigate() closes the modal.
	query := s.Query
	scope := s.Scope
	typ := s.Type
	rows := append([]Item(nil), s.Rows...)
	lanes := s.Lanes
	loaded := s.Loaded
	submitted := s.Submitted
	errMsg := s.Error
	selIdx := s.Selection

	a.navigate("检索结果", a.project)
	a.searchOrigin = origin

	s.Query = query
	s.Scope = scope
	s.Type = typ
	if s.Type == "" {
		s.Type = "全部"
	}
	s.Rows = rows
	s.Lanes = lanes
	s.Loaded = loaded
	s.Submitted = submitted
	s.Error = errMsg

	visible := a.visibleSearchRows()
	paneState := a.state()
	paneState.Items = visible
	paneState.Loaded = loaded
	if len(visible) > 0 {
		idx := max(0, min(selIdx, len(visible)-1))
		a.selectItem(paneState, visible[idx])
	} else if !loaded && !s.Loading {
		a.submitUnifiedSearch()
	}
}

func (a *App) searchResultsHeaderMeta() string {
	s := &a.searchUI
	rows := a.visibleSearchRows()
	q := strings.TrimSpace(s.Query)
	if s.Loading {
		return "搜索中…"
	}
	if q == "" {
		return fmt.Sprintf("最近 %d 条", len(rows))
	}
	keywords := 0
	for _, row := range rows {
		if row.SearchMatch == "关键词匹配" {
			keywords++
		}
	}
	return fmt.Sprintf("“%s” · 关键词 %d / 语义 %d", q, keywords, len(rows)-keywords)
}

func (a *App) searchResultsPage(c *ui.Context, paneState *pane) {
	s := &a.searchUI
	rows := a.visibleSearchRows()
	// Keep pane selection aligned with visible rows when search completes or filters change.
	if len(rows) > 0 {
		found := false
		if paneState.Selected != nil {
			for _, r := range rows {
				if r.key() == paneState.Selected.key() {
					found = true
					break
				}
			}
		}
		if !found {
			a.selectItem(paneState, rows[0])
		}
	} else if !s.Loading && paneState.Selected != nil {
		paneState.Selected = nil
	}

	readingSplit(c, "search-results-split", &a.layout.ProjectListWidth, func() {
		a.searchResultsListPane(c, paneState, rows)
	}, func() {
		a.searchResultsDetailPane(c, paneState)
	})
}

func (a *App) searchResultsListPane(c *ui.Context, paneState *pane, rows []Item) {
	s := &a.searchUI
	ui.Column(c.Key("search-results-list-pane")).Fill().Background(tok.Bg).Children(func() {
		// Top tools: search input + scope segmented + project-only chip
		ui.Column(c).Shrink(0).Padding(sp3, sp4).Gap(sp2).
			BorderWidth(0, 0, 1, 0).BorderColor(tok.Border).Children(func() {
			ui.Row(c).FillWidth().Height(30).Padding(0, sp2).Gap(6).AlignItems(ui.Center).
				Background(tok.Fill1).Radius(radius).Border(1, tok.Border).Children(func() {
				ui.Icon(c, v4IconSearch).FontSize(14).TextColor(tok.Text3).Shrink(0)
				inp := ui.TextInput(c.Key("sr-query-input"), &s.Query).
					Label("检索结果关键词").Placeholder("输入关键词过滤并高亮…").
					Grow(1).MinWidth(0).FontSize(fsFootnote).
					Background(ui.Transparent).BorderWidth(0).
					OnSubmit(a.submitUnifiedSearch)
				if inp.Changed() {
					s.HistoryDirty = true
					a.cancelChangedSearch()
				}
			})
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(sp2).Children(func() {
				labels := []string{"全部", "知识", "原始记录"}
				displayLabels := []string{"全部", "记忆", "原始记录"}
				selected := 0
				for n, label := range labels {
					if s.Type == label {
						selected = n
					}
				}
				v4Segmented(c, "sr-scope-seg", "检索结果类型筛选", &selected, displayLabels, func() {
					s.Type = labels[selected]
					s.Scroll.Y = 0
				})
				ui.Spacer(c)
				if proj, ok := a.searchTargetProject(); ok {
					active := s.Scope == proj.ID
					v4ScopeChip(c, "sr-proj-chip", "仅当前项目", "仅 "+proj.Name, active, func() {
						if s.Scope == proj.ID {
							s.Scope = ""
						} else {
							s.Scope = proj.ID
						}
						a.submitUnifiedSearch()
					})
				}
			})
			if s.Error != "" {
				ui.Row(c).FillWidth().Gap(sp2).AlignItems(ui.Center).Children(func() {
					homeText(c, s.Error, homeMeta).TextColor(tok.Danger).MaxLines(2).Grow(1).MinWidth(0)
					v4OutlineButton(c, "重试", nil, a.retryUnifiedSearch).Disabled(s.Loading)
				})
			}
		})

		// Result list
		switch {
		case s.Loading:
			ui.Column(c).Grow(1).MinHeight(0).FillWidth().Padding(sp3, sp4).Label("搜索中…").Children(func() {
				v4SkeletonGroup(c, 5, 2, false, false)
			})
		case len(rows) == 0:
			msg := "没有匹配的记忆或原始记录"
			if strings.TrimSpace(s.Query) != "" && s.Submitted != a.searchSignature() {
				msg = "按回车搜索"
			}
			v4EmptyState(c, "sr-empty", msg, "")
		default:
			paneState.ListSelection = -1
			for n, item := range rows {
				if paneState.Selected != nil && paneState.Selected.key() == item.key() {
					paneState.ListSelection = n
					break
				}
			}
			paneState.WidgetList.Key = func(n int) any { return rows[n].key() }
			paneState.WidgetList.Label = func(n int) string {
				title, _ := searchItemPrimaryText(rows[n], s.Query)
				return title
			}
			paneState.WidgetList.Selected = &paneState.ListSelection
			q := strings.TrimSpace(s.Query)
			ui.Themed(c, listTheme).Grow(1).MinHeight(0).FillWidth().Background(ui.Transparent).Children(func() {
				ui.List(c.Key("sr-list"), &paneState.WidgetList, len(rows), func(n int) {
					i := rows[n]
					a.searchResultsListItem(c, i, q)
				}).Label("检索结果列表").Grow(1).MinHeight(0).TrackScroll(&paneState.ListScroll).
					Padding(sp1, sp2, sp3, sp2).Gap(2).OnChange(func() {
					if paneState.ListSelection < 0 || paneState.ListSelection >= len(rows) {
						return
					}
					paneState.History = nil
					a.selectItem(paneState, rows[paneState.ListSelection])
				})
			})
		}

		// List footer
		appFooter(c, "检索结果统计").Padding(0, sp4).Children(func() {
			homeText(c, fmt.Sprintf("共 %d 项", len(rows)), homeMeta).FontFeatures("tnum")
			ui.Spacer(c)
			homeText(c, "↑↓ 切换", homeMeta)
		})
	})
}

func (a *App) searchResultsListItem(c *ui.Context, i Item, query string) {
	title, preview := searchItemPrimaryText(i, query)
	isKnowledge := strings.Contains(i.Kind, "知识")
	ui.Column(c).FillWidth().MinWidth(0).Padding(10, sp3).Gap(6).Children(func() {
		displayBody := title
		if query != "" && preview != "" {
			displayBody = title + " — " + preview
		}
		weight := 400
		if !isKnowledge {
			weight = 500
		}
		v4HighlightInlineMarkdown(c, displayBody, query, fsFootnote, weight, 1.5, tok.Text1).MaxLines(3)
		ui.Row(c).FillWidth().MinWidth(0).AlignItems(ui.Center).Gap(sp2).Children(func() {
			if isKnowledge && a.itemIsUnread(i) {
				v4UnreadDot(c)
			}
			agentLogo(c, recentAgentIcon(i), 16)
			if projName := a.searchProjectName(i.Dataset); projName != "" {
				ui.Text(c, projName).Font("system-ui").FontSize(fsCaption).TextColor(tok.Text3).SingleLine().Shrink(0)
			}
			if isKnowledge {
				v4RecallChip(c, i)
				if fields := knowledgeBasisFields(i); len(fields) > 0 {
					v4CategoryChip(c, fields[0].Value)
				}
			} else {
				rawTag := "会话"
				if i.Session == nil && !strings.Contains(i.Kind, "会话") {
					rawTag = "文档"
				}
				v4CategoryChip(c, rawTag)
			}
			ui.Spacer(c)
			dateStr := displayDate(i.Date)
			if isKnowledge {
				dateStr = knowledgeDateLabel(i)
			}
			ui.Text(c, dateStr).Font("system-ui").FontSize(fsCaption).FontFeatures("tnum").TextColor(tok.Text3).SingleLine().Shrink(0)
		})
	})
}

func (a *App) searchResultsDetailPane(c *ui.Context, paneState *pane) {
	q := strings.TrimSpace(a.searchUI.Query)
	ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Background(tok.Bg).Children(func() {
		if paneState.Selected == nil {
			v4EmptyState(c, "sr-reader-empty", "选择一条检索结果查看详情", "")
			appFooter(c, "检索结果阅读栏").Padding(0, sp6).Children(func() {
				homeText(c, "检索结果", homeMeta)
			})
			return
		}
		i := paneState.Selected
		key := i.key()
		isKnowledge := strings.Contains(i.Kind, "知识")

		// Meta toolbar
		appToolbar(c, "检索结果内容工具栏", func() {
			ui.Row(c).FillWidth().MaxWidth(readerMaxW).AlignItems(ui.Center).Gap(sp2).Children(func() {
				agentLogo(c, recentAgentIcon(*i), 18)
				meta := sourceAgentName(*i)
				if projName := a.searchProjectName(i.Dataset); projName != "" {
					meta += " · " + projName
				}
				if isKnowledge {
					meta += " · " + knowledgeDateLabel(*i)
				} else {
					meta += " · " + displayDate(i.Date)
				}
				if paneState.Reading {
					meta += " · 正在读取…"
				}
				homeText(c, meta, homeBody).SingleLine()
				if isKnowledge {
					v4RecallChip(c, *i)
					knowledgeBasisTag(c, *i)
				} else {
					rawTag := "会话"
					if i.Session == nil && !strings.Contains(i.Kind, "会话") {
						rawTag = "文档"
					}
					v4CategoryChip(c, rawTag)
				}
				if i.SearchMatch != "" {
					ui.Text(c, i.SearchMatch).Font("system-ui").FontSize(fsCaption).FontWeight(500).TextColor(tok.Accent).SingleLine().Shrink(0)
				}
				ui.Spacer(c)
			})
		}).Height(44).Padding(0, sp10)

		scroll := paneState.DetailScroll[key]
		if scroll == nil {
			scroll = &ui.ScrollState{}
			paneState.DetailScroll[key] = scroll
		}
		if a.motionRuns.docScrollKey != "sr:"+key {
			a.motionRuns.docScrollKey = "sr:" + key
			a.motionRuns.docEnter++
		}
		ui.Scroll(c.Key("sr-detail-"+key)).Grow(1).MinHeight(0).TrackScroll(scroll).Padding(sp3, sp10, sp8, sp10).Children(func() {
			docKey := fmt.Sprintf("sr-doc-%d-%s", a.motionRuns.docEnter, key)
			doc := ui.Column(c.Key(docKey)).FillWidth().MaxWidth(readerMaxW).Gap(sp3)
			a.v4DocEnter(c, doc, "sr-doc", a.motionRuns.docEnter)
			doc.Children(func() {
				title, _ := searchItemPrimaryText(*i, q)
				if title != "" && !isKnowledge {
					v4HighlightInlineMarkdown(c, title, q, fsHeading, 600, 1.45, tok.Text1)
				}
				if paneState.Error != "" {
					homeText(c, "内容未刷新 · "+paneState.Error, homeMeta).TextColor(tok.Danger)
				}
				switch {
				case i.Detail != nil && q != "":
					a.renderHighlightedSessionDetail(c, paneState, i, q)
				case i.Detail == nil && q != "":
					presentation := itemPresentation(*i)
					body := strings.TrimSpace(presentation.VisibleBody)
					if body == "" {
						body = strings.TrimSpace(i.Preview)
					}
					switch {
					case body != "":
						a.renderHighlightedParagraphs(c, body, q)
						if presentation.Unrecognized {
							ui.Text(c, "原文格式未识别，保留原文").FontSize(fsCaption).TextColor(tok.Text3)
						}
					case paneState.Reading:
						v4SkeletonGroup(c, 3, 2, false, false)
					case i.ReadingState == "unavailable" || i.ReadingState == "failed":
						homeText(c, "内容暂不可读", homeMeta)
					default:
						a.projectReadingBody(c, paneState, i)
					}
				default:
					a.projectReadingBody(c, paneState, i)
				}
				if isKnowledge {
					knowledgeBasis(c, *i)
					if i.Source != "" || i.SourceDocument != "" {
						ui.Row(c).FillWidth().Margin(sp4, 0, 0, 0).BorderWidth(1, 0, 0, 0).BorderColor(tok.Border).
							Padding(sp4, 0, 0, 0).Gap(sp3).AlignItems(ui.Center).Children(func() {
							ui.Icon(c, v4IconDoc).FontSize(16).TextColor(tok.Text3).Shrink(0)
							ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
								ui.Text(c, "出处 · "+readingSourceLabel(*i)).Font("system-ui").FontSize(fsFootnote).FontWeight(550).TextColor(tok.Text1).MaxLines(2)
								sub := "官方关联材料"
								if i.Source != "" {
									sub = sessionIdentity(i.Source)
								}
								homeText(c, sub, homeMeta).SingleLine()
							})
							v4SmallButton(c, "在项目中打开", nil, func() { a.openSearchResult(*i) })
						})
					}
				}
			})
		})

		canCopy := i.BodyLoaded || i.Detail != nil || i.Preview != ""
		footLabel := "原始记录 · 会话主题"
		if isKnowledge {
			footLabel = "记忆 · 只读"
		}
		appFooter(c, "检索结果阅读栏").Padding(0, sp6).Gap(sp2).Children(func() {
			homeText(c, footLabel, homeMeta)
			ui.Spacer(c)
			if time.Now().Before(a.projectUI.CopyUntil) {
				homeText(c, a.projectUI.Copied+"已复制", homeMeta)
			}
			v4SmallButton(c, "在项目中打开", nil, func() { a.openSearchResult(*i) }).Disabled(i.ReadingState == "unavailable")
			v4OutlineButton(c, "复制正文", v4IconCopy, func() {
				c.WriteClipboard(sessionCopyBody(paneState, *i))
				a.copyFeedback("正文")
			}).Disabled(!canCopy)
		})
	})
}

func (a *App) renderHighlightedSessionDetail(c *ui.Context, s *pane, i *Item, query string) {
	paged := a.sessionHistoryControls(c, s, *i)
	if !paged {
		homeText(c, sessionCoverage(*i.Detail), homeBody).FontSize(fsFootnote)
	}
	if !paged && (len(i.Detail.QAs) < i.Detail.Count || len(i.Detail.Traces) < i.Detail.ToolCount) {
		homeText(c, "当前是最近记录，可读取按时间排列的完整会话。", homeMeta)
		projectLink(c, "读取完整会话", func() { a.loadSessionPage(s, 0, 0) }).Disabled(s.Reading)
	}
	qas := i.Detail.QAs
	traces := i.Detail.Traces
	if paged {
		if s.SessionTools {
			qas = nil
		} else {
			traces = nil
		}
	}
	for _, q := range qas {
		ui.Column(c).FillWidth().Padding(11, 14).Background(tok.Fill1).Border(1, tok.Border).Radius(radius).Gap(6).Children(func() {
			ui.Text(c, "用户").Font("system-ui").FontSize(fsCaption).FontWeight(500).TextColor(tok.Text3)
			a.renderHighlightedParagraphs(c, q.Question, query)
			if q.QuestionTruncated {
				ui.Text(c, "官方内容已截断").FontSize(fsCaption).TextColor(tok.Text3)
			}
		})
		sourceAgentLabel(c, *i)
		a.renderHighlightedParagraphs(c, q.Answer, query)
		if q.AnswerTruncated {
			ui.Text(c, "官方内容已截断").FontSize(fsCaption).TextColor(tok.Text3)
		}
	}
	for _, trace := range traces {
		t := trace
		key := i.key() + "/" + t.ID
		dotState := "ok"
		if strings.Contains(strings.ToLower(t.Status), "err") || strings.Contains(strings.ToLower(t.Status), "fail") || t.Error != "" {
			dotState = "fail"
		}
		ui.Row(c.Key("sr-tool-"+key)).FillWidth().Padding(6, sp2).Radius(radiusSmall).
			Background(tok.Fill1).AlignItems(ui.Center).Gap(sp2).Children(func() {
			v4StatusDot(c, dotState)
			ui.Text(c, t.Name).Font("SF Mono").FontSize(fsCaption).FontWeight(500).TextColor(tok.Text1).SingleLine().Shrink(0)
			if t.Status != "" {
				homeText(c, t.Status, homeMeta).SingleLine()
			}
			if t.Error != "" {
				v4HighlightInlineMarkdown(c, t.Error, query, fsCaption, 400, 1.4, tok.Danger).MaxLines(2).Grow(1).MinWidth(0)
			}
		})
	}
	if paged {
		a.sessionLoadMore(c, s, *i)
	}
}

func (a *App) renderHighlightedParagraphs(c *ui.Context, body, query string) {
	blocks := strings.Split(strings.TrimSpace(body), "\n\n")
	for idx, block := range blocks {
		p := strings.TrimSpace(block)
		if p == "" {
			continue
		}
		v4HighlightInlineMarkdown(c, p, query, fsReading, 400, 1.8, tok.Text1).Key(fmt.Sprintf("sr-para-%d", idx))
	}
}
