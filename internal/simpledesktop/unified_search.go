package simpledesktop

import (
	"context"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"
)

var searchWrapperPrefixRe = regexp.MustCompile(`^(?:Lesson-Type:\s*\S+|State-Key:\s*\S+|(?:#\s*)?Session learning \(session [^\s)]+\)|Session ID:\s*\S+)\s*`)

func stripSearchWrapperPrefixes(s string) string {
	out := strings.TrimSpace(s)
	for {
		next := strings.TrimSpace(searchWrapperPrefixRe.ReplaceAllString(out, ""))
		if next == out {
			return out
		}
		out = next
	}
}

type unifiedSearchState struct {
	HistoryDirty                         bool
	Open, Focus, Loading, Loaded         bool
	Query, Scope, Type, Error, Submitted string
	Rows                                 []Item
	Lanes                                searchLaneResult
	Selection                            int
	KeyboardSelection                    bool
	Scroll                               ui.ScrollState
	Reveal                               bool
	lastSelIndY                          float32
	request                              uint64
	lastPublishSeq                       uint64
	cancel                               context.CancelFunc
}

func (a *App) searchCatalog(scope string) []Project {
	if scope == "@common" {
		return append([]Project(nil), a.Config.Common...)
	}
	if scope == "" {
		return overviewProjects(a.Config)
	}
	for _, p := range a.Config.Projects {
		if p.ID == scope {
			return []Project{p}
		}
	}
	return nil
}
func (a *App) searchSignature() string {
	s := &a.searchUI
	v := a.readingIdentity + "/" + strings.TrimSpace(s.Query) + "/" + s.Scope
	for _, p := range a.searchCatalog(s.Scope) {
		v += "/" + p.ID + ":" + p.Dataset
	}
	return v
}
func (a *App) openUnifiedSearch(restore bool) {
	s := &a.searchUI
	if s.Open {
		s.Focus = true
		return
	}
	a.projectUI.InfoOpen = false
	a.projectUI.MenuOpen = false
	hasCachedSearch := strings.TrimSpace(s.Query) != "" && (s.Loaded || len(s.Rows) > 0 || s.Submitted != "")
	if !restore && !hasCachedSearch {
		s.Scope = ""
		if a.page == "知识" || a.page == "图谱" {
			if p, ok := a.current(); ok && p.ID != "" {
				s.Scope = p.ID
			}
		}
		s.Query = ""
		s.Type = "全部"
	}
	if s.Type == "" {
		s.Type = "全部"
	}
	s.Open = true
	s.Focus = true
	s.Reveal = true
	if strings.TrimSpace(s.Query) == "" {
		a.submitUnifiedSearch()
	}
}
func (c *searchController) closeUnifiedSearch() {
	s := &c.searchUI
	s.HistoryDirty = false
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	s.request++
	s.Open = false
	s.Loading = false
}
func (c *searchController) invalidateUnifiedSearch(dataset string) {
	s := &c.searchUI
	c.closeUnifiedSearch()
	s.Lanes = searchLaneResult{}
	if dataset == "" {
		s.Rows = nil
		s.Loaded = false
		s.Submitted = ""
		return
	}
	rows := s.Rows[:0]
	for _, i := range s.Rows {
		if i.Dataset != dataset {
			rows = append(rows, i)
		}
	}
	s.Rows = rows
	s.Submitted = ""
}
func (a *App) submitUnifiedSearch() {
	a.runUnifiedSearch(false)
}

func (a *App) retryUnifiedSearch() {
	a.runUnifiedSearch(true)
}

func (a *App) runUnifiedSearch(retry bool) {
	s := &a.searchUI
	query := strings.TrimSpace(s.Query)

	if s.Loading && s.Submitted == a.searchSignature() {
		return
	}
	if query != "" && !retry {
		a.rememberManualSearch()
	}
	previous := searchLaneResult{}
	if retry && s.Submitted == a.searchSignature() {
		previous = s.Lanes
	}
	if s.cancel != nil {
		s.cancel()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	s.cancel = cancel
	s.request++
	s.lastPublishSeq = 0
	request, authEpoch := s.request, a.authEpoch
	signature := a.searchSignature()
	s.Submitted = signature
	s.Loading = true
	s.Loaded = false
	s.Error = ""
	s.Selection = 0
	s.KeyboardSelection = false
	projects := a.searchCatalog(s.Scope)
	scope := s.Scope
	client := a.Client
	control, hasControl := a.accessClient()
	identity := a.readingIdentity
	readingID, _ := readingIdentity(Client{Config: client.Config})
	recallStartedAt := a.overviewState.V2RecallStartedAt
	publishRows := func(seq uint64, lanes searchLaneResult, rows []Item, err error, done bool) {
		a.Update(func() {
			if request != s.request || authEpoch != a.authEpoch || signature != a.searchSignature() || identity != a.readingIdentity {
				return
			}
			if seq < s.lastPublishSeq {
				return
			}
			s.lastPublishSeq = seq
			prevSelectedKey := ""
			if s.KeyboardSelection && s.Selection >= 0 {
				if prevVis := a.visibleSearchRows(); s.Selection < len(prevVis) {
					prevSelectedKey = prevVis[s.Selection].key()
				}
			}
			if done {
				s.Loading = false
			}
			s.Lanes = lanes
			if err != nil {
				if readingUnavailable(err) {
					a.InvalidateReading("")
					return
				}
				if query == "" {
					s.Error = "最近内容读取失败"
					if done {
						s.Loading = false
					}
					return
				}
				if done || lanes.KeywordTimedOut {
					s.Error = lanes.message()
				}
			} else if done {
				s.Error = ""
			}
			// Preserve official identity; stable grouping does not invent document ids.
			out := []Item{}
			seen := map[string]bool{}
			for _, knowledge := range []bool{true, false} {
				for _, i := range rows {
					if strings.Contains(i.Kind, "知识") == knowledge && !seen[i.key()] {
						out = append(out, i)
						seen[i.key()] = true
					}
				}
			}
			s.Rows = out
			if !s.Loaded || len(out) > 0 {
				s.Reveal = true
			}
			if query == "" {
				s.Loaded = err == nil
			} else {
				s.Loaded = err == nil || lanes.hasAnySuccess()
			}
			if s.KeyboardSelection && prevSelectedKey != "" {
				nextVis := a.visibleSearchRows()
				relocated := false
				for idx, row := range nextVis {
					if row.key() == prevSelectedKey {
						s.Selection = idx
						relocated = true
						break
					}
				}
				if !relocated {
					if len(nextVis) == 0 {
						s.Selection = 0
					} else if s.Selection >= len(nextVis) {
						s.Selection = len(nextVis) - 1
					}
				}
			} else if !s.KeyboardSelection {
				s.Scroll.Y = 0
			}
		})
	}
	go func() {
		defer cancel()
		if len(projects) == 0 {
			publishRows(1, searchLaneResult{MetadataOK: true, SemanticOK: true, KeywordOK: true}, []Item{}, nil, true)
			return
		}
		if query == "" {
			if hasControl {
				rows, usedV2, err := recentSearchItemsV2(ctx, control, projects, scope, readingID, recallStartedAt)
				if usedV2 {
					publishRows(1, searchLaneResult{}, rows, err, true)
					return
				}
			}
			rows, err := a.recentSearchItems(ctx, client, projects, signature)
			publishRows(1, searchLaneResult{}, rows, err, true)
			return
		}
		hasV2Search := false
		if hasControl {
			var probeErr error
			hasV2Search, probeErr = control.probeFeature(ctx, v2FeatureSearch)
			if probeErr != nil {
				publishRows(1, searchLaneResult{MetadataError: probeErr}, nil, probeErr, true)
				return
			}
		}
		if hasControl && hasV2Search {
			projectID := ""
			if scope != "" && scope != "@common" && len(projects) == 1 {
				projectID = projects[0].ID
			}
			allowedDatasets := make(map[string]bool, len(projects))
			for _, p := range projects {
				allowedDatasets[p.Dataset] = true
			}
			var mu sync.Mutex
			var seq uint64
			lanes := previous
			publishProgressive := func(done bool) {
				mu.Lock()
				seq++
				mySeq := seq
				snap := lanes
				mu.Unlock()
				publishRows(mySeq, snap, snap.rows(query), snap.err(), done)
			}
			var wg sync.WaitGroup
			if !lanes.MetadataOK {
				wg.Add(1)
				go func() {
					defer wg.Done()
					resp, err := control.searchMetadataV2(ctx, readingID, query, "all", 30, projectID)
					var items []Item
					if err == nil {
						items = make([]Item, 0, len(resp.Items))
						for _, row := range resp.Items {
							if row.DocID == "" {
								continue
							}
							if !allowedDatasets[row.DatasetID] {
								continue
							}
							item := row.toItem(recallStartedAt)
							if readingID == "" {
								item.ServerUnreadKnown = false
								item.ServerUnread = false
							}
							items = append(items, item)
						}
					}
					mu.Lock()
					lanes.Metadata = items
					lanes.MetadataError = err
					lanes.MetadataOK = err == nil
					mu.Unlock()
					publishProgressive(false)
				}()
			}
			if !lanes.SemanticOK {
				wg.Add(1)
				go func() {
					defer wg.Done()
					items, err := client.search(ctx, projects, query)
					mu.Lock()
					lanes.Semantic = items
					lanes.SemanticError = err
					lanes.SemanticOK = err == nil
					mu.Unlock()
					publishProgressive(false)
				}()
			}
			if !lanes.KeywordOK {
				wg.Add(1)
				go func() {
					defer wg.Done()
					lexCtx, lexCancel := context.WithTimeout(ctx, lexicalSearchSoftTimeout)
					items, err := client.searchType(lexCtx, projects, query, "CHUNKS_LEXICAL")
					timedOut := err != nil && ctx.Err() == nil && (lexCtx.Err() == context.DeadlineExceeded || strings.Contains(err.Error(), context.DeadlineExceeded.Error()))
					lexCancel()
					mu.Lock()
					lanes.Keyword = items
					lanes.KeywordError = err
					lanes.KeywordOK = err == nil
					lanes.KeywordTimedOut = timedOut
					mu.Unlock()
					publishProgressive(false)
				}()
			}
			wg.Wait()
			publishProgressive(true)
			return
		}
		lanes := client.searchLanes(ctx, projects, query, previous)
		publishRows(1, lanes, lanes.rows(query), lanes.err(), true)
	}()
}
func (a *App) searchSubmitOrOpen() {
	s := &a.searchUI
	if s.KeyboardSelection && !s.Loading && s.Loaded && s.Submitted == a.searchSignature() && len(a.visibleSearchRows()) > 0 {
		rows := a.visibleSearchRows()
		a.openSearchResult(rows[min(s.Selection, len(rows)-1)])
		return
	}
	a.submitUnifiedSearch()
}
func (a *App) openSearchResult(i Item) {
	if i.ReadingState == "unavailable" {
		return
	}
	origin := a.readingLocation()
	if a.searchOrigin != nil {
		origin = a.searchOrigin
	}
	page, index, kind := "知识", -1, "原始记录"
	if strings.Contains(i.Kind, "知识") {
		kind = "知识"
	}
	for n, p := range a.Config.Projects {
		if p.Dataset == i.Dataset {
			index = n
			break
		}
	}
	if index < 0 {
		for n, p := range a.Config.Common {
			if p.Dataset == i.Dataset {
				page = "通用知识"
				index = n
				break
			}
		}
	}
	if index < 0 {
		a.searchUI.Error = "结果范围已失效，请重新搜索"
		return
	}
	a.navigateKind(page, index, kind)
	a.searchOrigin = origin
	target := a.state()
	a.searchPane = target
	a.searchAnchor = &i
	if !containsReadingItem(target.Items, i.key()) {
		target.Items = append([]Item{i}, target.Items...)
	}
	a.selectItem(target, i)
	if i.SourceDocument != "" && i.Document == nil {
		a.resolveSearchDocument(target, i)
	}
}
func containsReadingItem(rows []Item, key string) bool {
	for _, i := range rows {
		if i.key() == key {
			return true
		}
	}
	return false
}
func (a *App) resolveSearchDocument(s *pane, i Item) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	a.searchResolveCancel = cancel
	client, epoch, authEpoch := a.Client, a.epoch, a.authEpoch
	go func() {
		defer cancel()
		result, err := client.documentInfoByID(ctx, i.Dataset, i.SourceDocument)
		a.Update(func() {
			if epoch != a.epoch || authEpoch != a.authEpoch || a.searchPane != s || a.searchAnchor == nil || a.searchAnchor.key() != i.key() || s.Selected == nil || s.Selected.key() != i.key() {
				return
			}
			if err != nil {
				if readingUnavailable(err) {
					a.InvalidateReading(i.Dataset)
				}
				return
			}
			a.searchAnchor = &result
			rows := []Item{result}
			for _, row := range s.Items {
				if row.key() != i.key() && row.key() != result.key() {
					rows = append(rows, row)
				}
			}
			s.Items = rows
			a.selectItem(s, result)
		})
	}()
}

func (a *App) returnToSearch() {
	v := a.searchOrigin
	a.restoreReadingLocation(v)
	a.openUnifiedSearch(true)
}
func (a *App) unifiedSearchTrigger(c *ui.Context) {
	b := ui.ButtonBase(c.Key("unified-search-trigger")).Role(ui.RoleButton).Cursor(ui.CursorPointer).
		Label("搜索记忆与原始记录").Width(260).Height(28).Padding(0, sp2).Gap(sp2).
		Background(tok.Fill1).Radius(radius).AlignItems(ui.Center).Shrink(0)
	borderCol := ui.Transparent
	if b.Hovered() {
		borderCol = tok.BorderStrong
	}
	b.Border(1, b.AnimateColor("search-border", borderCol, v4Fast))
	v4ButtonPress(b, 0.98)
	b.Children(func() {
		ui.Icon(c, v4IconSearch).FontSize(14).TextColor(tok.Text3).Shrink(0)
		ui.Text(c, "搜索记忆与原始记录").Font("system-ui").FontSize(fsFootnote).FontWeight(400).TextColor(tok.Text3).SingleLine()
		ui.Spacer(c)
		ui.Text(c, "⌘ K").Font("system-ui").FontSize(fsCaption).FontWeight(400).TextColor(tok.Text4).SingleLine()
	}).OnClick(a.OpenSearch)
}
func (a *App) visibleSearchRows() []Item {
	// Submitted identifies the in-flight request, not the rows' provenance.
	// Never present old rows under newly typed text while that request runs,
	// while allowing progressive v2 lanes to render as soon as Lane 1 resolves.
	if (a.searchUI.Loading && !a.searchUI.Loaded) || a.searchUI.Submitted != a.searchSignature() {
		return nil
	}
	result := []Item{}
	for _, i := range a.searchUI.Rows {
		knowledge := strings.Contains(i.Kind, "知识")
		if a.searchUI.Type == "知识" && !knowledge || a.searchUI.Type == "原始记录" && knowledge {
			continue
		}
		result = append(result, i)
	}
	return result
}
func (a *App) cancelChangedSearch() {
	s := &a.searchUI
	if s.cancel != nil {
		s.cancel()
	}
	s.request++
	s.Loading = false
	s.Error = ""
	s.Selection = 0
	s.KeyboardSelection = false
	s.Loaded = false
	s.Rows = nil
	s.Lanes = searchLaneResult{}
	s.Submitted = ""
}
func searchRowText(i Item) (string, string) {
	body := i.Preview
	if body == "" {
		body = itemPresentation(i).VisibleBody
	}
	// Technical headers are presentation-only; keep the original source bytes.
	lines := strings.Split(body, "\n")
	clean := make([]string, 0, len(lines))
	for _, line := range lines {
		v := strings.TrimSpace(line)
		if searchWrapperPrefixRe.MatchString(v) {
			rest := stripSearchWrapperPrefixes(v)
			if rest == "" {
				continue
			}
			clean = append(clean, rest)
			continue
		}
		clean = append(clean, line)
	}
	preview := stripSearchWrapperPrefixes(strings.Join(clean, "\n"))
	title := i.Title
	if title == "检索片段" || readableName(title) == "" || strings.HasPrefix(title, "Session ID: ") || searchWrapperPrefixRe.MatchString(strings.TrimSpace(title)) {
		title = firstReadingSentence(preview)
	}
	if title == "" {
		if strings.Contains(i.Kind, "知识") {
			title = "记忆"
		} else {
			title = "原始记录"
		}
	}
	if strings.TrimSpace(preview) == strings.TrimSpace(title) {
		preview = ""
	}
	return title, preview
}
func recentSearchItemsV2(ctx context.Context, control ProjectControlClient, projects []Project, scope, readingID, recallStartedAt string) ([]Item, bool, error) {
	if len(projects) == 0 {
		return []Item{}, true, nil
	}
	hasV2Search, probeErr := control.probeFeature(ctx, v2FeatureSearch)
	if probeErr != nil {
		return nil, true, probeErr
	}
	if !hasV2Search {
		return nil, false, nil
	}
	projectID := ""
	if len(projects) == 1 && scope != "" && scope != "@common" {
		projectID = projects[0].ID
	}
	allowed := make(map[string]bool, len(projects))
	for _, p := range projects {
		allowed[p.Dataset] = true
	}
	resp, err := control.searchRecentV2(ctx, readingID, 20, projectID)
	if err == nil {
		result := make([]Item, 0, 12)
		seen := map[string]bool{}
		kCount := 0
		for _, row := range resp.KnowledgeItems {
			if row.DocID == "" || !allowed[row.DatasetID] {
				continue
			}
			item := row.toItem(recallStartedAt)
			if readingID == "" {
				item.ServerUnreadKnown = false
				item.ServerUnread = false
			}
			if seen[item.key()] {
				continue
			}
			seen[item.key()] = true
			result = append(result, item)
			kCount++
			if kCount >= 6 {
				break
			}
		}
		sCount := 0
		for _, row := range resp.SessionItems {
			if row.SessionID == "" || !allowed[row.DatasetID] {
				continue
			}
			item := row.toItem()
			if seen[item.key()] {
				continue
			}
			seen[item.key()] = true
			result = append(result, item)
			sCount++
			if sCount >= 6 {
				break
			}
		}
		return result, true, nil
	}
	if isControlFailClosedV2(err) {
		return nil, true, err
	}
	return nil, false, nil
}

func (a *App) recentSearchItems(ctx context.Context, client Client, projects []Project, generation string) ([]Item, error) {
	if len(projects) == 0 {
		return []Item{}, nil
	}
	rows := []Item{}
	for _, p := range projects {
		docs, err := client.overviewDocuments(ctx, p)
		if err != nil {
			return nil, err
		}
		for _, d := range docs {
			if d.knowledge() {
				copy := d
				rows = append(rows, Item{ID: d.ID, Dataset: p.Dataset, Kind: "知识", Title: d.title(), Date: d.date(), Source: d.session(), Document: &copy})
			}
		}
		records, _, err := client.projectRecords(ctx, p, 0, "原始记录")
		if err != nil {
			return nil, err
		}
		rows = append(rows, records...)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		a := dateOrder(rows[i].Date)
		b := dateOrder(rows[j].Date)
		if a.Equal(b) {
			return rows[i].key() < rows[j].key()
		}
		return a.After(b)
	})
	result := []Item{}
	seen := map[string]bool{}
	counts := map[bool]int{}
	for _, i := range rows {
		knowledge := strings.Contains(i.Kind, "知识")
		if counts[knowledge] >= 3 || seen[i.key()] {
			continue
		}
		seen[i.key()] = true
		counts[knowledge]++
		result = append(result, i)
	}
	var workers sync.WaitGroup
	failures := make([]error, len(result))
	for n := range result {
		if result[n].Document != nil && readableDocumentTitle(*result[n].Document) != "" {
			continue
		}
		workers.Add(1)
		go func(n int) {
			defer workers.Done()
			i, err := a.titles.read(ctx, client, result[n], generation, false, true)
			if err == nil {
				result[n] = i
			} else {
				result[n].ReadingState = "failed"
				failures[n] = err
			}
		}(n)
	}
	workers.Wait()
	for _, err := range failures {
		if readingUnavailable(err) {
			return nil, err
		}
	}
	return result, nil
}
