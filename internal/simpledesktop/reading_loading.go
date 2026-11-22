package simpledesktop

import (
	"context"
	"fmt"
	"strings"
)

func (a *App) startRecentTitles(rows []Item) {
	if a.Update == nil {
		return
	}
	candidates := make([]Item, 0, min(len(rows), maxTitlePreheatWindow))
	for _, row := range rows {
		if row.Document == nil || row.ReadingState == "ready" || row.Preview != "" || readableDocumentTitle(*row.Document) != "" {
			continue
		}
		candidates = append(candidates, row)
		if len(candidates) >= maxTitlePreheatWindow {
			break
		}
	}
	if len(candidates) == 0 {
		return
	}
	var scope strings.Builder
	fmt.Fprintf(&scope, "%d/%d/%d/%s", a.epoch, a.authEpoch, a.overviewState.generation, a.readingIdentity)
	for _, i := range candidates {
		fmt.Fprintf(&scope, "/%v", keyForReading(a.readingIdentity, i, fmt.Sprint(a.overviewState.generation)))
	}
	if a.recentScope == scope.String() {
		return
	}
	a.recentScope = scope.String()
	if a.recentCancel != nil {
		a.recentCancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.recentCancel = cancel
	a.recentCtx = ctx
	a.recentRequest++
	request, epoch, authEpoch := a.recentRequest, a.epoch, a.authEpoch
	client := a.Client
	generation := fmt.Sprint(a.overviewState.generation)
	for _, row := range candidates {
		i := row
		revision := a.readingRevisions[i.key()]
		go func() {
			result, err := a.titles.read(ctx, client, i, generation, false, true)
			a.Update(func() {
				if request != a.recentRequest || epoch != a.epoch || authEpoch != a.authEpoch || revision != a.readingRevisions[i.key()] || ctx.Err() != nil || !a.syncReadingIdentity() {
					return
				}
				updateSlice := func(target []Item) bool {
					matched := false
					for n, current := range target {
						if current.key() == i.key() {
							matched = true
							if err != nil {
								target[n].ReadingState = "failed"
								continue
							}
							target[n].Title = overviewReadingTitle(result)
							target[n].ReadingState = "ready"
						}
					}
					return matched
				}
				matchedSnap := updateSlice(a.overviewState.Snapshot.Knowledge)
				matchedDay := updateSlice(a.overviewState.DayItems)
				if err != nil && (matchedSnap || matchedDay) {
					a.handleReadingFailure(i, err)
				}
			})
		}()
	}
}

func (a *App) readingGeneration(s *pane) string { return fmt.Sprintf("%d/%d", a.epoch, s.request) }

func (a *App) retryRecentTitle(i Item) {
	ctx := context.Background()
	if a.recentCtx != nil {
		ctx = a.recentCtx
	}
	request, epoch, authEpoch, revision := a.recentRequest, a.epoch, a.authEpoch, a.readingRevisions[i.key()]
	client := a.Client
	generation := fmt.Sprint(a.overviewState.generation)
	go func() {
		result, err := a.titles.read(ctx, client, i, generation, true, false)
		a.Update(func() {
			if request != a.recentRequest || epoch != a.epoch || authEpoch != a.authEpoch || revision != a.readingRevisions[i.key()] || ctx.Err() != nil || !a.syncReadingIdentity() {
				return
			}
			a.handleReadingFailure(i, err)
			apply := func(target []Item) {
				for n, row := range target {
					if row.key() == i.key() {
						if err != nil {
							target[n].ReadingState = "failed"
							continue
						}
						target[n].Title = overviewReadingTitle(result)
						target[n].ReadingState = "ready"
					}
				}
			}
			apply(a.overviewState.Snapshot.Knowledge)
			apply(a.overviewState.DayItems)
		})
	}()
}

// SetReadingPolicyRevision is called on the UI thread after a policy refresh.
// Include this revision even when URL and credential bytes have not changed.
func (a *App) SetReadingPolicyRevision(revision string) {
	if a.Client.PolicyRevision == revision {
		return
	}
	a.Client.PolicyRevision = revision
	a.syncReadingIdentity()
}
func (a *App) handleReadingFailure(i Item, err error) {
	if !readingUnavailable(err) {
		return
	}
	a.withdrawCommonEditor(i.Dataset)
	status, ok := httpStatusCode(err)
	if (ok && status == 401) || strings.Contains(err.Error(), "identity changed") || strings.Contains(err.Error(), "credential unavailable") {
		a.InvalidateReading("")
	} else {
		a.unavailableReadingItem(i)
	}
}
func (a *App) cancelTitles(s *pane) {
	if s.titleCancel != nil {
		s.titleCancel()
		s.titleCancel = nil
	}
	s.titleRequest++
}
const maxTitlePreheatWindow = 30

func titlePreheatRange(s *pane) (start, end int) {
	total := len(s.Items)
	if total == 0 {
		return 0, 0
	}
	if total <= maxTitlePreheatWindow {
		return 0, total
	}
	first, last := s.WidgetList.Visible()
	if first < 0 || first >= total || last < first {
		return 0, maxTitlePreheatWindow
	}
	start = max(0, first-5)
	end = min(total, start+maxTitlePreheatWindow)
	if last >= end {
		start = max(0, min(first, last-maxTitlePreheatWindow+1))
		end = min(total, start+maxTitlePreheatWindow)
	}
	return start, end
}

func (a *App) startTitles(s *pane) {
	a.cancelTitles(s)
	ctx, cancel := context.WithCancel(context.Background())
	s.titleCancel = cancel
	s.titleCtx = ctx
	request, epoch, authEpoch := s.titleRequest, a.epoch, a.authEpoch
	generation := a.readingGeneration(s)
	client := a.Client
	start, end := titlePreheatRange(s)
	scheduled := 0
	for n := start; n < end && scheduled < maxTitlePreheatWindow; n++ {
		row := s.Items[n]
		if row.ReadingState == "ready" || row.Preview != "" {
			continue
		}
		if row.Session == nil && (row.Kind != "知识" || row.Document == nil || (a.page != "通用知识" && readableDocumentTitle(*row.Document) != "")) {
			continue
		}
		scheduled++
		i := row
		revision := a.readingRevisions[i.key()]
		if s.Items[n].ReadingState != "ready" {
			s.Items[n].ReadingState = "pending"
		}
		go func() {
			result, err := a.titles.read(ctx, client, i, generation, false, true)
			a.Update(func() {
				if epoch != a.epoch || authEpoch != a.authEpoch || request != s.titleRequest || revision != a.readingRevisions[i.key()] || ctx.Err() != nil {
					return
				}
				if !a.syncReadingIdentity() {
					return
				}
				a.handleReadingFailure(i, err)
				for idx, current := range s.Items {
					if current.key() == i.key() {
						if err != nil {
							s.Items[idx].ReadingState = "failed"
							s.Items[idx].ReadingError = "标题读取失败"
							if readingUnavailable(err) {
								s.Items[idx].ReadingState = "unavailable"
							}
							return
						}
						// Do not put preheated raw in every row. The bounded cache owns it.
						title := result.Title
						if a.page == "知识" && result.Session == nil {
							title = projectDisplayTitle(result)
							if title == "" {
								// List rows need a content preview; the detail heading
								// only uses an actual document name/label.
								body := itemPresentation(result).VisibleBody
								title = firstReadingSentenceLimit(body, len([]rune(body)))
							}
						} else if a.page == "通用知识" {
							if draft, ok := commonReadingParts(result); ok {
								title = draft.Title
							}
						}
						s.Items[idx].Title = title
						s.Items[idx].SessionSummary = result.SessionSummary
						s.Items[idx].ReadingState = "ready"
						s.Items[idx].ReadingError = ""
						if s.Selected != nil && s.Selected.key() == i.key() && !s.Selected.BodyLoaded {
							s.Selected.Title = title
						}
						return
					}
				}
			})
		}()
	}
}

func (a *App) unavailableReadingItem(i Item) {
	a.projectUI.InfoOpen = false
	for n, r := range a.searchUI.Rows {
		if r.Dataset == i.Dataset && (r.ID == i.ID || r.SourceDocument == i.ID) {
			a.searchUI.Rows[n].Body = ""
			a.searchUI.Rows[n].Preview = ""
			a.searchUI.Rows[n].Title = "内容已失效"
			a.searchUI.Rows[n].ReadingState = "unavailable"
		}
	}
	a.titles.InvalidateObject(i.Dataset, i.ID)
	if a.readingRevisions == nil {
		a.readingRevisions = map[string]uint64{}
	}
	a.readingRevisions[i.key()]++
	for _, s := range a.panes {
		if s.SourcePage != nil && s.SourcePage.ID == i.ID && s.SourcePage.Dataset == i.Dataset {
			s.SourcePage = nil
		}
		if s.Selected != nil && s.Selected.key() == i.key() {
			s.Selected = nil
			s.Reading = false
		}
		history := s.History[:0]
		for _, h := range s.History {
			if h.key() != i.key() {
				history = append(history, h)
			}
		}
		s.History = history
		for n, row := range s.Items {
			if row.key() == i.key() {
				s.Items[n].Body = ""
				s.Items[n].Preview = ""
				s.Items[n].BodyLoaded = false
				s.Items[n].RecallStats = nil
				s.Items[n].ReadingState = "unavailable"
				s.Items[n].ReadingError = "内容不可用"
				if row.Document != nil {
					docCopy := *row.Document
					docCopy.Metadata = nil
					s.Items[n].Document = &docCopy
					s.Items[n].Title = docCopy.title()
				}
			}
		}
	}
	for n, row := range a.overviewState.Snapshot.Knowledge {
		if row.key() == i.key() {
			a.overviewState.Snapshot.Knowledge[n].Body = ""
			a.overviewState.Snapshot.Knowledge[n].Preview = ""
			a.overviewState.Snapshot.Knowledge[n].BodyLoaded = false
			if row.Document != nil {
				docCopy := *row.Document
				docCopy.Metadata = nil
				a.overviewState.Snapshot.Knowledge[n].Document = &docCopy
				a.overviewState.Snapshot.Knowledge[n].Title = docCopy.title()
			}
			a.overviewState.Snapshot.Knowledge[n].ReadingState = "unavailable"
		}
	}
}
func (a *App) retryReadingTitle(s *pane, i Item) {
	// A retry is user initiated and bounded to this object, with no retry loop.
	parent := s.titleCtx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	for n, row := range s.Items {
		if row.key() == i.key() {
			s.Items[n].ReadingState = "pending"
			s.Items[n].ReadingError = ""
		}
	}
	request, epoch, authEpoch, generation := s.titleRequest, a.epoch, a.authEpoch, a.readingGeneration(s)
	client := a.Client
	revision := a.readingRevisions[i.key()]
	go func() {
		defer cancel()
		result, err := a.titles.read(ctx, client, i, generation, true, false)
		a.Update(func() {
			if request != s.titleRequest || epoch != a.epoch || authEpoch != a.authEpoch || revision != a.readingRevisions[i.key()] || ctx.Err() != nil || !a.syncReadingIdentity() {
				return
			}
			a.handleReadingFailure(i, err)
			for n, row := range s.Items {
				if row.key() == i.key() {
					if err != nil {
						s.Items[n].ReadingState = "failed"
						return
					}
					s.Items[n].Title = result.Title
					s.Items[n].ReadingState = "ready"
					s.Items[n].ReadingError = ""
				}
			}
		})
	}()
}

// InvalidateReading must be called by policy/credential integration when access is
// revoked. It removes raw data and copy affordances, including nested history, and
// fences every in-flight request via authEpoch + per-pane/overview/queue counters.
func (a *App) InvalidateReading(dataset string) {
	a.authEpoch++
	a.withdrawCommonEditor(dataset)
	a.invalidateUnifiedSearch(dataset)
	if dataset == "" || (a.searchAnchor != nil && a.searchAnchor.Dataset == dataset) {
		a.searchAnchor = nil
		a.searchPane = nil
	}
	a.projectUI.InfoOpen = false
	a.titles.Invalidate(dataset)
	a.recentScope = ""
	if a.recentCancel != nil {
		a.recentCancel()
		a.recentCancel = nil
	}
	a.recentRequest++

	// Fence overview, activity, & day in-flight requests so late callbacks cannot repopulate revoked data.
	a.overviewState.generation++
	a.overviewState.activityGeneration++
	a.overviewState.dayRequest++
	if a.overviewState.cancel != nil {
		a.overviewState.cancel()
		a.overviewState.cancel = nil
	}
	if a.overviewState.activityCancel != nil {
		a.overviewState.activityCancel()
		a.overviewState.activityCancel = nil
	}
	if a.overviewState.dayCancel != nil {
		a.overviewState.dayCancel()
		a.overviewState.dayCancel = nil
	}
	if a.overviewState.recallCancel != nil {
		a.overviewState.recallCancel()
		a.overviewState.recallCancel = nil
	}
	a.overviewState.Loading = false
	a.overviewState.ActivityLoading = false
	a.overviewState.DayLoading = false
	a.overviewState.cachedGroupedEvents = nil
	if dataset == "" {
		a.overviewState.DayItems = nil
		a.overviewState.DayNextCursor = ""
		a.overviewState.DayHasMore = false
		a.overviewState.DayTotal = 0
		a.overviewState.V2DayBuckets = nil
		a.overviewState.V2TotalKnowledge = 0
		a.overviewState.Events = nil
		a.overviewState.History = nil
		a.overviewState.HistoryLoaded = false
		a.overviewState.ActivityLoaded = false
		a.overviewState.More = false
	} else {
		if len(a.overviewState.DayItems) > 0 {
			keptDay := make([]Item, 0, len(a.overviewState.DayItems))
			for _, i := range a.overviewState.DayItems {
				if i.Dataset != dataset {
					keptDay = append(keptDay, i)
				}
			}
			a.overviewState.DayItems = keptDay
			a.overviewState.DayNextCursor = ""
			a.overviewState.DayHasMore = false
		}
		if len(a.overviewState.Events) > 0 {
			keptEvents := make([]overviewEvent, 0, len(a.overviewState.Events))
			for _, ev := range a.overviewState.Events {
				if ev.Record.Dataset == dataset {
					continue
				}
				if len(ev.Failures) > 0 {
					keptFailures := make([]activityRow, 0, len(ev.Failures))
					for _, f := range ev.Failures {
						if f.Dataset != dataset {
							keptFailures = append(keptFailures, f)
						}
					}
					if len(keptFailures) == 0 {
						continue
					}
					ev.Failures = keptFailures
				}
				if ev.Recall != nil {
					keptHits := make([]RecallHit, 0, len(ev.Recall.Hits))
					for _, h := range ev.Recall.Hits {
						if h.Dataset != dataset {
							keptHits = append(keptHits, h)
						}
					}
					if len(keptHits) == 0 {
						continue
					}
					rcCopy := *ev.Recall
					rcCopy.Hits = keptHits
					ev.Recall = &rcCopy
				}
				keptEvents = append(keptEvents, ev)
			}
			a.overviewState.Events = keptEvents
		}
		if len(a.overviewState.History) > 0 {
			keptHistory := make([]overviewEvent, 0, len(a.overviewState.History))
			for _, ev := range a.overviewState.History {
				if ev.Record.Dataset != dataset {
					keptHistory = append(keptHistory, ev)
				}
			}
			a.overviewState.History = keptHistory
		}
	}
	for n, i := range a.overviewState.Snapshot.Knowledge {
		if dataset == "" || i.Dataset == dataset {
			a.overviewState.Snapshot.Knowledge[n].Body = ""
			a.overviewState.Snapshot.Knowledge[n].Preview = ""
			a.overviewState.Snapshot.Knowledge[n].BodyLoaded = false
			a.overviewState.Snapshot.Knowledge[n].Detail = nil
			a.overviewState.Snapshot.Knowledge[n].ReadingState = ""
			if i.Document != nil {
				docCopy := *i.Document
				docCopy.Metadata = nil
				a.overviewState.Snapshot.Knowledge[n].Document = &docCopy
				a.overviewState.Snapshot.Knowledge[n].Title = docCopy.title()
			}
		}
	}
	for _, s := range a.panes {
		a.cancelTitles(s)
		paneMatches := dataset == "" ||
			(len(s.Items) > 0 && s.Items[0].Dataset == dataset) ||
			(s.Selected != nil && s.Selected.Dataset == dataset) ||
			(s.HomeAnchor != nil && s.HomeAnchor.Dataset == dataset) ||
			(s.SubagentParent != nil && s.SubagentParent.Dataset == dataset)
		if paneMatches {
			if s.cancel != nil {
				s.cancel()
				s.cancel = nil
			}
			s.request++
			s.Loading = false
			s.Locating = false
			s.Knowledge.Appending = false
			s.Knowledge.Prepending = false
			s.RawAppending = false
			s.Knowledge.NextCursor = ""
			s.Knowledge.PrevCursor = ""
			s.Knowledge.HasPrev = false
			s.RawNextCursor = ""
			s.CommonNextCursor = ""
			s.CommonPrevCursor = ""
			s.CommonHasPrev = false
			s.CommonNav = nil
			s.CommonCursors = nil
			s.CommonPageIndex = 0
		}
		if len(s.RawCatalog) > 0 && (dataset == "" || s.RawCatalog[0].Dataset == dataset) {
			s.RawCatalog = nil
			s.More = false
		}
		if s.HomeAnchor != nil && (dataset == "" || s.HomeAnchor.Dataset == dataset) {
			s.HomeAnchor = nil
		}
		if s.SubagentParent != nil && (dataset == "" || s.SubagentParent.Dataset == dataset) {
			s.SubagentParent = nil
			s.SubagentParentPage = nil
			s.SessionSubagents = false
		}
		if s.SourcePage != nil && (dataset == "" || s.SourcePage.Dataset == dataset) {
			s.SourcePage = nil
		}
		if dataset == "" || (s.Selected != nil && s.Selected.Dataset == dataset) {
			if s.readCancel != nil {
				s.readCancel()
				s.readCancel = nil
			}
			s.readRequest++
			if s.Selected != nil {
				s.Selected = nil
				s.Reading = false
				s.Error = "内容已失效或无权读取，请刷新"
			} else {
				s.Reading = false
			}
		}
		history := s.History[:0]
		for _, i := range s.History {
			if dataset != "" && i.Dataset != dataset {
				history = append(history, i)
			}
		}
		s.History = history
		for n, i := range s.Items {
			if dataset == "" || i.Dataset == dataset {
				s.Items[n].Body = ""
				s.Items[n].Preview = ""
				s.Items[n].BodyLoaded = false
				s.Items[n].Detail = nil
				s.Items[n].RecallStats = nil
				s.Items[n].Subagents = nil
				s.Items[n].SubagentsHasMore = false
				s.Items[n].SubagentsNextCursor = ""
				s.Items[n].SubagentTotalCount = 0
				s.Items[n].SubagentsLoading = false
				s.Items[n].SessionSummary = ""
				s.Items[n].ReadingState = "unavailable"
				s.Items[n].ReadingError = "内容不可用"
				if i.Document != nil {
					docCopy := *i.Document
					docCopy.Metadata = nil
					s.Items[n].Document = &docCopy
					s.Items[n].Title = docCopy.title()
				}
			}
		}
		for n, i := range s.Knowledge.Catalog {
			if dataset == "" || i.Dataset == dataset {
				s.Knowledge.Catalog[n].Body = ""
				s.Knowledge.Catalog[n].Preview = ""
				s.Knowledge.Catalog[n].BodyLoaded = false
				s.Knowledge.Catalog[n].Detail = nil
				s.Knowledge.Catalog[n].RecallStats = nil
				s.Knowledge.Catalog[n].ReadingState = "unavailable"
				s.Knowledge.Catalog[n].ReadingError = "内容不可用"
				if i.Document != nil {
					docCopy := *i.Document
					docCopy.Metadata = nil
					s.Knowledge.Catalog[n].Document = &docCopy
					s.Knowledge.Catalog[n].Title = docCopy.title()
				}
			}
		}
	}
	for k, q := range a.access.Queues {
		if q == nil {
			continue
		}
		if dataset == "" || strings.HasSuffix(k, "/"+dataset) {
			q.Request++
			q.Loading = false
			q.UnreadKnowledge = nil
			q.UnreadHasMore = false
			q.ServerUnreadKnown = false
			q.ServerUnreadCount = 0
			q.ServerUnreadStateRev = 0
			q.Documents = nil
			q.PendingDocumentsCount = 0
			q.StateVersion++
		}
	}
}
func (a *App) cachedBaseReadingIdentity() string {
	if a.baseReadingIdentity != "" &&
		a.baseIdentityURL == a.Client.Config.ServiceURL &&
		a.baseIdentityTokenFile == a.Client.Config.TokenFile {
		return a.baseReadingIdentity
	}
	_, base, err := readingIdentityPair(a.Client)
	if err != nil {
		return ""
	}
	a.updateBaseReadingIdentity(base, a.Client.Config.ServiceURL, a.Client.Config.TokenFile)
	return base
}
func (a *App) syncReadingIdentity() bool {
	identity, base, err := readingIdentityPair(a.Client)
	if err != nil {
		identity = "unavailable"
		base = ""
	}
	a.updateBaseReadingIdentity(base, a.Client.Config.ServiceURL, a.Client.Config.TokenFile)
	if identity != a.readingIdentity {
		if a.commonEdit.Existing != nil {
			a.withdrawCommonEditor("")
			a.commonEdit.Message = "连接或权限配置已变化，先前正文已撤下；请重新读取，原操作回执保留。"
		}
		a.readingIdentity = identity
		a.epoch++
		for _, s := range a.panes {
			a.cancelTitles(s)
			if s.cancel != nil {
				s.cancel()
			}
			s.Loading = false
			s.Loaded = false
		}
		a.InvalidateReading("")
		return false
	}
	return err == nil
}

// Keep presentation metadata for an explicitly unchanged content revision while
// title hydration runs. Never carry raw bodies or unknown document revisions.
func preserveResolvedReadingTitles(rows, previous []Item) {
	old := make(map[string]Item, len(previous))
	for _, item := range previous {
		if item.ReadingState == "ready" {
			old[item.key()] = item
		}
	}
	for n, row := range rows {
		before, ok := old[row.key()]
		if !ok {
			continue
		}
		same := false
		if row.Document != nil && before.Document != nil && (row.Document.ContentHash != "" || row.Document.Updated != "") {
			same = keyForReading("", row, "") == keyForReading("", before, "") && readableDocumentTitle(*row.Document) == ""
		} else if row.Session != nil && before.Session != nil {
			same = row.Session.Updated != "" && row.Session.Updated == before.Session.Updated
		}
		if same {
			rows[n].Title, rows[n].SessionSummary = before.Title, before.SessionSummary
			rows[n].ReadingState = before.ReadingState
		}
	}
}
