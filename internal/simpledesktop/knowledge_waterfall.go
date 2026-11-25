package simpledesktop

import (
	"context"
	"fmt"
	"strings"
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
	Initialized, Detail  bool
	Catalog              []Item
	Columns              map[string]int
	Measurements         map[string]knowledgeCardMeasurement
	Copied               string
	CopyUntil            time.Time
	RecallPending        int
	RecallRequest        uint64
	RecallChecked        map[string]bool
	RecallNext           time.Time
	SortByRecalls        bool
	CountsLoaded         bool
	RecallOrderPending   bool
	CountsError          string
	RecallFilter         map[string]bool
	RecallAgentID        string
	RecallEventID        string
	CategoryFilter       string
	UnreadOnly           bool
	V2Mode               bool
	NextCursor           string
	PrevCursor           string
	HasPrev              bool
	Prepending           bool
	TotalFiltered        int
	ServerCategoryCounts map[string]int
	ServerBaseTotal      int
	CatalogVersion       string
	NewCatalogAvailable  bool
	Appending            bool
}

// enforceListSlidingWindow caps active list items to maxActiveListItems (300)
// from the front and releases evicted per-item UI state so deep cursor
// pagination stays bounded in heap and frame time.
func enforceListSlidingWindow(s *pane) {
	enforceListSlidingWindowDir(s, true)
}

func enforceListSlidingWindowDir(s *pane, trimFront bool) {
	if s == nil || len(s.Items) <= maxActiveListItems {
		return
	}
	drop := len(s.Items) - maxActiveListItems
	var evicted []Item
	kept := make([]Item, maxActiveListItems)
	if trimFront {
		evicted = s.Items[:drop]
		copy(kept, s.Items[drop:])
		if s.Knowledge.V2Mode {
			s.Knowledge.HasPrev = true
			s.Knowledge.PrevCursor = ""
		}
		if s.ListSelection >= 0 {
			s.ListSelection -= drop
			if s.ListSelection < 0 {
				s.ListSelection = -1
			}
		}
	} else {
		evicted = s.Items[maxActiveListItems:]
		copy(kept, s.Items[:maxActiveListItems])
		if s.Knowledge.V2Mode {
			s.More = true
			s.Knowledge.NextCursor = ""
		}
		if s.ListSelection >= maxActiveListItems {
			s.ListSelection = -1
		}
	}
	s.Items = kept
	selectedKey := ""
	if s.Selected != nil {
		selectedKey = s.Selected.key()
	}
	for _, row := range evicted {
		key := row.key()
		if key == selectedKey {
			continue
		}
		delete(s.Knowledge.Measurements, key)
		delete(s.Knowledge.Columns, key)
		delete(s.DetailScroll, key)
		delete(s.DetailScroll, key+"/tools")
		delete(s.DetailScroll, key+"/subagents")
		delete(s.Technical, key)
	}
}

type knowledgeCardMeasurement struct {
	Body, State   string
	Width, Height float32
}

var knowledgeQuote = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="currentColor"><path d="M3 13c0-6 3-9 8-10v4c-3 1-4 3-4 5h4v9H3zm12 0c0-6 3-9 8-10v4c-3 1-4 3-4 5h4v9h-8z"/></svg>`))
var knowledgeSource = homeSVG(`<path d="m10 13 4-4M8 16l-2 2a4 4 0 0 1-6-6l5-5a4 4 0 0 1 6 0M16 8l2-2a4 4 0 0 1 6 6l-5 5a4 4 0 0 1-6 0" transform="translate(1 0) scale(.9)"/>`)

func (a *App) loadKnowledgeFeed(s *pane) {
	a.syncReadingIdentity()
	p, ok := a.current()
	update := a.Update
	if !ok || update == nil {
		return
	}
	a.cancelTitles(s)
	if s.cancel != nil {
		s.cancel()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	s.cancel = cancel
	s.request++
	request, epoch, authEpoch := s.request, a.epoch, a.authEpoch
	s.Knowledge.Initialized, s.Loading, s.Error = true, true, ""
	s.Knowledge.Appending = false
	s.Knowledge.Prepending = false
	s.Knowledge.NewCatalogAvailable = false
	s.Knowledge.RecallRequest++
	s.Knowledge.RecallPending = 0
	s.Knowledge.RecallChecked = nil
	client := a.Client
	control, hasControl := a.accessClient()
	stateDir := a.readingStateDir()
	accessProjects := append([]AccessProject(nil), a.access.Directory.Projects...)
	hasCurrentProject := false
	for _, ap := range accessProjects {
		if ap.ID == p.ID && ap.Dataset == p.Dataset {
			hasCurrentProject = true
			break
		}
	}
	if !hasCurrentProject && p.ID != "" && p.Dataset != "" {
		accessProjects = append(accessProjects, AccessProject{ID: p.ID, Name: p.Name, Dataset: p.Dataset})
	}
	sortByRecalls := s.Knowledge.SortByRecalls
	categoryFilter := s.Knowledge.CategoryFilter
	unreadOnly := s.Knowledge.UnreadOnly
	recallAgentID := s.Knowledge.RecallAgentID
	recallEventID := s.Knowledge.RecallEventID
	readingID, _ := readingIdentity(Client{Config: client.Config})
	recallStartedAt := a.overviewState.V2RecallStartedAt
	go func() {
		defer cancel()
		failClosed := func(err error) {
			update(func() {
				if epoch != a.epoch || authEpoch != a.authEpoch || request != s.request || !a.syncReadingIdentity() {
					return
				}
				s.Loading = false
				s.Error = controlUserError(err)
				if readingUnavailable(err) {
					a.InvalidateReading(p.Dataset)
					s.Knowledge.Catalog = nil
				}
			})
		}
		if !hasControl {
			failClosed(fmt.Errorf("控制面服务不可用，无法加载知识目录"))
			return
		}
		if p.Dataset == "" {
			failClosed(fmt.Errorf("项目正在登记，内容归属尚未确认"))
			return
		}
		if len(accessProjects) > 0 {
			if migErr := a.migrateReadingStateV2Snapshot(ctx, control, readingID, stateDir, accessProjects); migErr != nil {
				if ctx.Err() != nil {
					return
				}
				if isControlAuthDenied(migErr) || a.hasPendingLocalReadingMigration(control.Config.URL, stateDir, readingID, accessProjects) {
					failClosed(migErr)
					return
				}
			}
		}
		sortParam := "date"
		if sortByRecalls {
			sortParam = "recall"
		}
		v2Resp, v2Err := control.projectKnowledgeV2(ctx, readingID, p.ID, sortParam, categoryFilter, unreadOnly, recallAgentID, recallEventID, knowledgeBatch, "")
		if v2Err == nil {
			mapped := make([]Item, 0, len(v2Resp.Items))
			for _, row := range v2Resp.Items {
				if row.DocID == "" {
					continue
				}
				mapped = append(mapped, row.toItem(recallStartedAt))
			}
			baseTotal := v2Resp.TotalFiltered
			if categoryFilter != "" && len(v2Resp.CategoryCounts) > 0 {
				sum := 0
				for _, cnt := range v2Resp.CategoryCounts {
					sum += cnt
				}
				baseTotal = sum
			}
			update(func() {
				if epoch != a.epoch || authEpoch != a.authEpoch || request != s.request || !a.syncReadingIdentity() {
					return
				}
				s.Loading = false
				s.Error = ""
				s.Knowledge.V2Mode = true
				s.Knowledge.NextCursor = v2Resp.NextCursor
				s.Knowledge.PrevCursor = v2Resp.PrevCursor
				s.Knowledge.HasPrev = v2Resp.HasPrev
				s.Knowledge.Prepending = false
				s.Knowledge.TotalFiltered = v2Resp.TotalFiltered
				s.Knowledge.ServerCategoryCounts = v2Resp.CategoryCounts
				s.Knowledge.ServerBaseTotal = baseTotal
				s.Knowledge.CatalogVersion = v2Resp.CatalogVersion
				s.Knowledge.NewCatalogAvailable = false
				s.Knowledge.CountsLoaded = true
				s.Knowledge.CountsError = ""
				s.Knowledge.Catalog = nil
				s.Items = mapped
				s.More = v2Resp.HasMore
				s.Loaded = true
				a.syncV2ProjectUnreadCount(p.ID, p.Dataset, v2Resp.UnreadCount, v2Resp.UnreadStateRev, v2Resp.CatalogVersion)
				a.hydrateKnowledgeCards(s)
				if s.Selected == nil && len(s.Items) > 0 && !s.Locating {
					a.selectItem(s, s.Items[0])
				}
			})
			return
		}
		failClosed(v2Err)
	}()
}

func (a *App) appendKnowledgeBatch(s *pane) {
	if s.Loading || s.Error != "" || !s.More {
		return
	}
	if s.Knowledge.V2Mode {
		if s.Knowledge.Appending || a.Update == nil {
			return
		}
		if s.Knowledge.NextCursor == "" && len(s.Items) == 0 {
			return
		}
		p, ok := a.current()
		control, hasControl := a.accessClient()
		if !ok || !hasControl || p.ID == "" {
			return
		}
		s.Knowledge.Appending = true
		request, epoch, authEpoch := s.request, a.epoch, a.authEpoch
		opts := v2KnowledgePageOpts{Cursor: s.Knowledge.NextCursor, Direction: "next"}
		if opts.Cursor == "" && len(s.Items) > 0 {
			opts.AnchorDocID = s.Items[len(s.Items)-1].ID
		}
		sortParam := "date"
		if s.Knowledge.SortByRecalls {
			sortParam = "recall"
		}
		categoryFilter := s.Knowledge.CategoryFilter
		unreadOnly := s.Knowledge.UnreadOnly
		recallAgentID := s.Knowledge.RecallAgentID
		recallEventID := s.Knowledge.RecallEventID
		readingID, _ := readingIdentity(Client{Config: a.Client.Config})
		recallStartedAt := a.overviewState.V2RecallStartedAt
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			resp, err := control.projectKnowledgePageV2(ctx, readingID, p.ID, sortParam, categoryFilter, unreadOnly, recallAgentID, recallEventID, knowledgeBatch, opts)
			a.Update(func() {
				if epoch != a.epoch || authEpoch != a.authEpoch || request != s.request || !a.syncReadingIdentity() {
					return
				}
				s.Knowledge.Appending = false
				if err != nil {
					if isControlInvalidCursor(err) {
						// Explicitly reset expired cursor to page 1 under the same filter.
						s.Knowledge.NextCursor = ""
						s.Knowledge.PrevCursor = ""
						s.Knowledge.HasPrev = false
						s.More = false
						s.Error = ""
						a.loadKnowledgeFeed(s)
						return
					}
					s.Error = controlUserError(err)
					if readingUnavailable(err) {
						a.InvalidateReading(p.Dataset)
					}
					return
				}
				seen := make(map[string]bool, len(s.Items))
				for _, existing := range s.Items {
					seen[existing.key()] = true
				}
				for _, row := range resp.Items {
					if row.DocID == "" {
						continue
					}
					item := row.toItem(recallStartedAt)
					if !seen[item.key()] {
						s.Items = append(s.Items, item)
						seen[item.key()] = true
					}
				}
				enforceListSlidingWindowDir(s, true)
				s.Knowledge.NextCursor = resp.NextCursor
				s.More = resp.HasMore
				s.Knowledge.TotalFiltered = resp.TotalFiltered
				a.hydrateKnowledgeCards(s)
			})
		}()
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

func (a *App) prependKnowledgeBatch(s *pane) {
	if s.Loading || s.Error != "" || !s.Knowledge.V2Mode || !s.Knowledge.HasPrev || s.Knowledge.Prepending || a.Update == nil || len(s.Items) == 0 {
		return
	}
	p, ok := a.current()
	control, hasControl := a.accessClient()
	if !ok || !hasControl || p.ID == "" {
		return
	}
	s.Knowledge.Prepending = true
	request, epoch, authEpoch := s.request, a.epoch, a.authEpoch
	opts := v2KnowledgePageOpts{Cursor: s.Knowledge.PrevCursor, Direction: "prev"}
	if opts.Cursor == "" {
		opts.AnchorDocID = s.Items[0].ID
	}
	sortParam := "date"
	if s.Knowledge.SortByRecalls {
		sortParam = "recall"
	}
	categoryFilter := s.Knowledge.CategoryFilter
	unreadOnly := s.Knowledge.UnreadOnly
	recallAgentID := s.Knowledge.RecallAgentID
	recallEventID := s.Knowledge.RecallEventID
	readingID, _ := readingIdentity(Client{Config: a.Client.Config})
	recallStartedAt := a.overviewState.V2RecallStartedAt
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		resp, err := control.projectKnowledgePageV2(ctx, readingID, p.ID, sortParam, categoryFilter, unreadOnly, recallAgentID, recallEventID, knowledgeBatch, opts)
		a.Update(func() {
			if epoch != a.epoch || authEpoch != a.authEpoch || request != s.request || !a.syncReadingIdentity() {
				return
			}
			s.Knowledge.Prepending = false
			if err != nil {
				if isControlInvalidCursor(err) {
					s.Knowledge.NextCursor = ""
					s.Knowledge.PrevCursor = ""
					s.Knowledge.HasPrev = false
					s.More = false
					s.Error = ""
					a.loadKnowledgeFeed(s)
					return
				}
				s.Error = controlUserError(err)
				if readingUnavailable(err) {
					a.InvalidateReading(p.Dataset)
				}
				return
			}
			seen := make(map[string]bool, len(s.Items))
			for _, existing := range s.Items {
				seen[existing.key()] = true
			}
			prefix := make([]Item, 0, len(resp.Items))
			for _, row := range resp.Items {
				if row.DocID == "" {
					continue
				}
				item := row.toItem(recallStartedAt)
				if !seen[item.key()] {
					prefix = append(prefix, item)
					seen[item.key()] = true
				}
			}
			if len(prefix) > 0 {
				s.Items = append(prefix, s.Items...)
				if s.ListSelection >= 0 {
					s.ListSelection += len(prefix)
				}
				enforceListSlidingWindowDir(s, false)
			}
			s.Knowledge.PrevCursor = resp.PrevCursor
			s.Knowledge.HasPrev = resp.HasPrev
			s.Knowledge.TotalFiltered = resp.TotalFiltered
			a.hydrateKnowledgeCards(s)
		})
	}()
}

func (a *App) hydrateKnowledgeCards(s *pane) {
	if a.Update == nil {
		return
	}
	if s.titleCtx == nil || s.titleCtx.Err() != nil {
		s.titleCtx, s.titleCancel = context.WithCancel(context.Background())
	}
	ctx, epoch, authEpoch, request, generation := s.titleCtx, a.epoch, a.authEpoch, s.titleRequest, a.readingGeneration(s)
	client := a.Client
	v2Mode := s.Knowledge.V2Mode

	scheduleIndex := func(n int) bool {
		row := s.Items[n]
		if row.BodyLoaded || row.Preview != "" || row.ReadingState == "ready" || row.ReadingState == "pending" || row.ReadingState == "failed" || row.ReadingState == "unavailable" {
			return false
		}
		i, revision := row, a.readingRevisions[row.key()]
		s.Items[n].ReadingState = "pending"
		go func() {
			result, err := a.titles.read(ctx, client, i, generation, false, true)
			a.Update(func() {
				if ctx.Err() != nil || epoch != a.epoch || authEpoch != a.authEpoch || request != s.titleRequest || revision != a.readingRevisions[i.key()] || !a.syncReadingIdentity() {
					return
				}
				if err != nil {
					a.handleReadingFailure(i, err)
				}
				for idx, cur := range s.Items {
					if cur.key() == i.key() {
						if err != nil {
							s.Items[idx].ReadingState = "failed"
							if readingUnavailable(err) {
								s.Items[idx].ReadingState = "unavailable"
							}
							return
						}
						if v2Mode {
							// Keep full bodies out of list rows in v2 mode; bounded ReadingTitles
							// cache and s.Selected own full document bodies.
							visible := strings.TrimSpace(itemPresentation(result).VisibleBody)
							s.Items[idx].Preview = firstReadingSentenceLimit(visible, 240)
							s.Items[idx].ReadingState = "ready"
							if s.Items[idx].Title == "" {
								s.Items[idx].Title = result.Title
							}
						} else {
							s.Items[idx].Body, s.Items[idx].BodyLoaded, s.Items[idx].BodyTruncated, s.Items[idx].ReadingState = result.Body, true, result.BodyTruncated, "ready"
						}
						if s.Selected != nil && s.Selected.key() == i.key() && !s.Selected.BodyLoaded {
							updated := result
							s.Selected = &updated
							a.markKnowledgeRead(updated)
						}
						return
					}
				}
			})
		}()
		return true
	}

	start, end := titlePreheatRange(s)
	scheduled := 0
	for n := start; n < end && scheduled < maxTitlePreheatWindow; n++ {
		if scheduleIndex(n) {
			scheduled++
		}
	}
	if !v2Mode && scheduled < maxTitlePreheatWindow {
		for n := 0; n < len(s.Items) && scheduled < maxTitlePreheatWindow; n++ {
			if n >= start && n < end {
				continue
			}
			if scheduleIndex(n) {
				scheduled++
			}
		}
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
					ui.Row(c.Key("knowledge-new-catalog-banner")).FillWidth().Padding(sp1, sp4).Gap(sp2).AlignItems(ui.Center).Background(tok.AccentSofter).Shrink(0).Children(func() {
						ui.Text(c, "有新记忆可用").Font("system-ui").FontSize(fsCaption).FontWeight(500).TextColor(tok.Accent).SingleLine()
						ui.Spacer(c)
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
		if preview := strings.TrimSpace(s.Items[n].Preview); preview != "" {
			return preview
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
