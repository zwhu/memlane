package simpledesktop

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"
)

const (
	knowledgeBatch                = 30
	knowledgeInset        float32 = pageInset
	knowledgeGap          float32 = sectionGap
	knowledgeBodySize     float32 = fsReading
	knowledgeBodyLine     float32 = 22
	knowledgePreviewLines         = 3
)

// Only presentation metadata is retained beyond the displayed batch. Bodies
// remain process-local and use the existing shared, four-slot reading scheduler.
type knowledgeFeedState struct {
	Initialized, Detail bool
	Catalog             []Item
	Columns             map[string]int
	Measurements        map[string]knowledgeCardMeasurement
	Copied              string
	CopyUntil           time.Time
	RecallPending       int
	RecallRequest       uint64
	RecallChecked       map[string]bool
	RecallNext          time.Time
	SortByRecalls       bool
	CountsLoaded        bool
	RecallOrderPending  bool
	CountsError         string
	RecallFilter        map[string]bool
	CategoryFilter      string
	UnreadOnly          bool
}

type knowledgeCardMeasurement struct {
	Body, State   string
	Width, Height float32
}

var knowledgeQuote = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="currentColor"><path d="M3 13c0-6 3-9 8-10v4c-3 1-4 3-4 5h4v9H3zm12 0c0-6 3-9 8-10v4c-3 1-4 3-4 5h4v9h-8z"/></svg>`))
var knowledgeSource = homeSVG(`<path d="m10 13 4-4M8 16l-2 2a4 4 0 0 1-6-6l5-5a4 4 0 0 1 6 0M16 8l2-2a4 4 0 0 1 6 6l-5 5a4 4 0 0 1-6 0" transform="translate(1 0) scale(.9)"/>`)

func knowledgeCatalog(docs []Document) []Item {
	rows := []Item{}
	seen := map[string]bool{}
	for _, d := range docs {
		if !d.knowledge() {
			continue
		}
		copy := d
		i := Item{ID: d.ID, Dataset: d.Dataset, Kind: "知识", Date: d.date(), Source: d.session(), Document: &copy}
		if !seen[i.key()] {
			rows = append(rows, i)
			seen[i.key()] = true
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		x, y := dateOrder(rows[i].Date), dateOrder(rows[j].Date)
		if x.Equal(y) {
			return rows[i].key() < rows[j].key()
		}
		return x.After(y)
	})
	return rows
}

func (a *App) loadKnowledgeFeed(s *pane) {
	a.syncReadingIdentity()
	p, ok := a.current()
	if !ok || a.Update == nil {
		return
	}
	a.cancelTitles(s)
	if s.cancel != nil {
		s.cancel()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	s.cancel = cancel
	s.request++
	request, epoch := s.request, a.epoch
	s.Knowledge.Initialized, s.Loading, s.Error = true, true, ""
	s.Knowledge.RecallRequest++
	s.Knowledge.RecallPending = 0
	s.Knowledge.RecallChecked = nil
	wasLoaded := s.Loaded
	client := a.Client
	control, hasControl := a.accessClient()
	titles := a.titles
	genStr := a.readingGeneration(s)
	sortByRecalls := s.Knowledge.SortByRecalls
	go func() {
		defer cancel()
		var docs []Document
		var err error
		var counts *knowledgeRecallCounts
		var countsError error
		if p.Dataset == "" {
			err = fmt.Errorf("项目正在登记，内容归属尚未确认")
		} else {
			var wg sync.WaitGroup
			wg.Add(1)
			go func() {
				defer wg.Done()
				docs, err = client.overviewDocuments(ctx, p)
			}()
			if hasControl {
				wg.Add(1)
				go func() {
					defer wg.Done()
					counts, countsError = control.knowledgeRecallCounts(ctx, p.Dataset)
				}()
			}
			wg.Wait()
		}
		preheated := map[string]Item{}
		if err == nil && !wasLoaded {
			rows := knowledgeCatalog(docs)
			applyRecallCounts(rows, counts)
			sortKnowledgeRows(rows, sortByRecalls)
			preCtx, preCancel := context.WithTimeout(ctx, 600*time.Millisecond)
			topN := min(10, len(rows))
			var mu sync.Mutex
			var wg sync.WaitGroup
			for idx := 0; idx < topN; idx++ {
				item := rows[idx]
				wg.Add(1)
				go func() {
					defer wg.Done()
					if res, e := titles.read(preCtx, client, item, genStr, true, true); e == nil {
						mu.Lock()
						preheated[item.key()] = res
						mu.Unlock()
					}
				}()
			}
			wg.Wait()
			preCancel()
		}
		a.Update(func() {
			if epoch != a.epoch || request != s.request || !a.syncReadingIdentity() {
				return
			}
			s.Loading = false
			if err != nil {
				s.Error = err.Error()
				if readingUnavailable(err) {
					a.InvalidateReading(p.Dataset)
					s.Knowledge.Catalog = nil
				}
				return
			}
			rows := knowledgeCatalog(docs)
			applyRecallCounts(rows, counts)
			s.Knowledge.CountsLoaded = counts != nil
			s.Knowledge.RecallNext = time.Now().Add(30 * time.Second)
			s.Knowledge.CountsError = ""
			if countsError != nil {
				s.Knowledge.CountsError = countsError.Error()
			}
			sortKnowledgeRows(rows, s.Knowledge.SortByRecalls)
			if s.ListScroll.Y > 0 {
				byKey := map[string]Item{}
				for _, i := range rows {
					byKey[i.key()] = i
				}
				ordered := make([]Item, 0, len(rows))
				for _, old := range s.Knowledge.Catalog {
					if i, ok := byKey[old.key()]; ok {
						ordered = append(ordered, i)
						delete(byKey, old.key())
					}
				}
				for _, i := range rows {
					if _, ok := byKey[i.key()]; ok {
						ordered = append(ordered, i)
					}
				}
				rows = ordered
			}
			old := map[string]Item{}
			for _, i := range s.Items {
				old[i.key()] = i
			}
			s.Knowledge.Catalog = rows
			rows = a.filteredKnowledgeCatalog(s)
			count := min(max(knowledgeBatch, len(s.Items)), len(rows))
			s.Items = append([]Item(nil), rows[:count]...)
			for n, i := range s.Items {
				if pre, ok := preheated[i.key()]; ok {
					s.Items[n].Body, s.Items[n].BodyLoaded, s.Items[n].ReadingState = pre.Body, true, "ready"
				} else if before, ok := old[i.key()]; ok && before.BodyLoaded && keyForReading("", i, "") == keyForReading("", before, "") && i.Document != nil && (i.Document.Updated != "" || i.Document.ContentHash != "") {
					s.Items[n].Body, s.Items[n].BodyLoaded, s.Items[n].ReadingState = before.Body, true, "ready"
				}
			}
			s.More, s.Loaded = count < len(rows), true
			a.hydrateKnowledgeCards(s)
			if s.Selected == nil && len(s.Items) > 0 && !s.Locating {
				a.selectItem(s, s.Items[0])
			}
		})
	}()
}

func (a *App) appendKnowledgeBatch(s *pane) {
	if s.Loading || s.Error != "" || !s.More {
		return
	}
	rows := a.filteredKnowledgeCatalog(s)
	end := min(len(s.Items)+knowledgeBatch, len(rows))
	if end <= len(s.Items) {
		s.More = false
		return
	}
	s.Items = append(s.Items, rows[len(s.Items):end]...)
	s.More = end < len(rows)
	a.hydrateKnowledgeCards(s)
}

func (a *App) hydrateKnowledgeCards(s *pane) {
	if a.Update == nil {
		return
	}
	if s.titleCtx == nil || s.titleCtx.Err() != nil {
		s.titleCtx, s.titleCancel = context.WithCancel(context.Background())
	}
	ctx, epoch, request, generation := s.titleCtx, a.epoch, s.titleRequest, a.readingGeneration(s)
	client := a.Client
	for n, row := range s.Items {
		if row.BodyLoaded || row.ReadingState == "pending" || row.ReadingState == "failed" || row.ReadingState == "unavailable" {
			continue
		}
		i, revision := row, a.readingRevisions[row.key()]
		s.Items[n].ReadingState = "pending"
		go func() {
			result, err := a.titles.read(ctx, client, i, generation, false, true)
			a.Update(func() {
				if ctx.Err() != nil || epoch != a.epoch || request != s.titleRequest || revision != a.readingRevisions[i.key()] || !a.syncReadingIdentity() {
					return
				}
				if err != nil {
					a.handleReadingFailure(i, err)
				}
				for n, row := range s.Items {
					if row.key() == i.key() {
						if err != nil {
							s.Items[n].ReadingState = "failed"
							if readingUnavailable(err) {
								s.Items[n].ReadingState = "unavailable"
							}
							return
						}
						s.Items[n].Body, s.Items[n].BodyLoaded, s.Items[n].ReadingState = result.Body, true, "ready"
						if s.Selected != nil && s.Selected.key() == i.key() && !s.Selected.BodyLoaded {
							updated := s.Items[n]
							s.Selected = &updated
							a.markKnowledgeRead(updated)
						}
						return
					}
				}
			})
		}()
	}
}

func knowledgeCardBody(i Item) string {
	if !i.BodyLoaded {
		return ""
	}
	return strings.TrimSpace(itemPresentation(i).VisibleBody)
}

func knowledgeDateLabel(i Item) string {
	date := i.Date
	if i.Document != nil {
		if day, ok := i.Document.knowledgeDay(time.Local); ok {
			date = day
		} else {
			date = ""
		}
	}
	if date == "" {
		return "来源日期未知"
	}
	if t, ok := officialTime(date); ok {
		return t.In(time.Local).Format("2006-01-02")
	}
	return date
}

// knowledgePage renders the V4 Three-Pane project knowledge view:
// 360px list column (underline tabs + filter toolbar + keyboard-navigable list)
// beside the full reading pane.
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
	a.loadKnowledgeCardRecalls(s)
	if s.Knowledge.RecallOrderPending && s.ListScroll.Y <= 0 {
		a.reorderKnowledgeRange(s, max(knowledgeBatch, len(s.Items)), false)
	}
	ready := true
	for _, i := range s.Items {
		if !i.BodyLoaded && i.ReadingState != "failed" && i.ReadingState != "unavailable" {
			ready = false
		}
	}
	if s.More && s.Loaded && ready && s.ListScroll.MaxY > 0 && s.ListScroll.Y >= s.ListScroll.MaxY-160 {
		a.appendKnowledgeBatch(s)
	}

	readingSplit(c, "project-reading-split", &a.layout.ProjectListWidth, func() {
		ui.Column(c.Key("knowledge-list-pane")).Fill().Background(tok.Bg).Children(func() {
			ui.Column(c.Key("project-view-navigation")).Padding(0, sp4).BorderWidth(0, 0, 1, 0).BorderColor(tok.Border).Shrink(0).Children(func() {
				a.projectTabs(c)
			})
			bodyKey := fmt.Sprintf("knowledge-list-body-%d", a.motionRuns.projectPage)
			body := ui.Column(c.Key(bodyKey)).Grow(1).MinHeight(0).FillWidth()
			a.v4PageEnter(c, body, "knowledge-list-body", a.motionRuns.projectPage)
			body.Children(func() {
				a.knowledgeFilterBar(c, s)
				a.projectSessionDriftBanner(c, s)
				if s.Error != "" {
					ui.Row(c).Padding(sp1, sp4).Gap(sp2).AlignItems(ui.Center).Shrink(0).Children(func() {
						homeText(c, "读取未完成 · "+s.Error, homeMeta).MaxLines(2)
						projectLink(c, "重试读取记忆", func() { a.loadKnowledgeFeed(s) })
					})
				}
				a.knowledgeList(c, s)
			})
			totalFiltered := len(a.filteredKnowledgeCatalog(s))
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
		if listSelected != nil && listSelected.key() == item.key() {
			s.ListSelection = n
			break
		}
	}
	s.WidgetList.Key = func(n int) any { return s.Items[n].key() }
	s.WidgetList.KeepOffset = true
	s.WidgetList.Label = func(n int) string {
		if body := knowledgeCardBody(s.Items[n]); body != "" {
			return body
		}
		return s.Items[n].Title
	}
	s.WidgetList.Selected = &s.ListSelection
	if s.Selected != nil && s.RevealItem == s.Selected.key() && s.ListSelection >= 0 {
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
	itemKey := fmt.Sprintf("kitem-%d-%s", a.motionRuns.projectList, i.key())
	col := ui.Column(c.Key(itemKey)).FillWidth().MinWidth(0).Padding(10, sp3).Gap(6)
	a.v4StaggerEnter(c, col, "kitem-"+i.key(), index, a.motionRuns.projectList, false)
	col.Children(func() {
		body := knowledgeCardBody(i)
		switch {
		case i.ReadingState == "failed" || i.ReadingState == "unavailable":
			homeText(c, "记忆暂不可读", homeMeta)
			projectLink(c, "重试读取正文", func() {
				for n, row := range s.Items {
					if row.key() == i.key() {
						s.Items[n].ReadingState = ""
					}
				}
				a.hydrateKnowledgeCards(s)
			})
		case !i.BodyLoaded:
			preview := strings.TrimSpace(i.Title)
			ui.Column(c).FillWidth().MinHeight(54).Children(func() {
				if preview == "" {
					ui.Column(c).FillWidth().Gap(6).Children(func() {
						ui.Box(c).FillWidth().Height(11).Radius(radiusSmall).Background(tok.Fill2)
						ui.Box(c).Width(180).Height(11).Radius(radiusSmall).Background(tok.Fill1)
					})
				} else {
					v4InlineMarkdown(c, preview, fsFootnote, 400, 1.55, tok.Text2).MaxLines(knowledgePreviewLines)
				}
			})
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
