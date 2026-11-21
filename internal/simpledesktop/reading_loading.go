package simpledesktop

import (
	"context"
	"fmt"
	"strings"
	"time"
)

func (a *App) startRecentTitles(rows []Item) {
	if a.Update == nil {
		return
	}
	var scope strings.Builder
	fmt.Fprintf(&scope, "%d/%d/%s", a.epoch, a.overviewState.generation, a.readingIdentity)
	for _, i := range rows {
		fmt.Fprintf(&scope, "/%v", keyForReading(a.readingIdentity, i, fmt.Sprint(a.overviewState.generation)))
	}
	if a.recentScope == scope.String() {
		return
	}
	pending := make([]Item, 0, len(rows))
	for _, row := range rows {
		if row.Document == nil || readableDocumentTitle(*row.Document) != "" || row.ReadingState == "ready" {
			continue
		}
		pending = append(pending, row)
	}
	if len(pending) == 0 {
		a.recentScope = scope.String()
		if a.recentCancel != nil {
			a.recentCancel()
			a.recentCancel = nil
		}
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
	request, epoch := a.recentRequest, a.epoch
	client := a.Client
	generation := fmt.Sprint(a.overviewState.generation)
	for _, row := range pending {
		i := row
		revision := a.readingRevisions[i.key()]
		go func() {
			result, err := a.titles.read(ctx, client, i, generation, false, true)
			a.Update(func() {
				if request != a.recentRequest || epoch != a.epoch || revision != a.readingRevisions[i.key()] || ctx.Err() != nil || !a.syncReadingIdentity() {
					return
				}
				for n, current := range a.overviewState.Snapshot.Knowledge {
					if current.key() == i.key() {
						if err != nil {
							a.overviewState.Snapshot.Knowledge[n].ReadingState = "failed"
							a.handleReadingFailure(i, err)
							return
						}
						a.overviewState.Snapshot.Knowledge[n].Title = overviewReadingTitle(result)
						a.overviewState.Snapshot.Knowledge[n].ReadingState = "ready"
					}
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
	request, epoch, revision := a.recentRequest, a.epoch, a.readingRevisions[i.key()]
	client := a.Client
	generation := fmt.Sprint(a.overviewState.generation)
	go func() {
		result, err := a.titles.read(ctx, client, i, generation, true, false)
		a.Update(func() {
			if request != a.recentRequest || epoch != a.epoch || revision != a.readingRevisions[i.key()] || ctx.Err() != nil || !a.syncReadingIdentity() {
				return
			}
			a.handleReadingFailure(i, err)
			for n, row := range a.overviewState.Snapshot.Knowledge {
				if row.key() == i.key() {
					if err != nil {
						a.overviewState.Snapshot.Knowledge[n].ReadingState = "failed"
						return
					}
					a.overviewState.Snapshot.Knowledge[n].Title = overviewReadingTitle(result)
					a.overviewState.Snapshot.Knowledge[n].ReadingState = "ready"
				}
			}
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
	request, epoch := s.titleRequest, a.epoch
	generation := a.readingGeneration(s)
	client := a.Client
	start, end := titlePreheatRange(s)
	scheduled := 0
	for n := start; n < end && scheduled < maxTitlePreheatWindow; n++ {
		row := s.Items[n]
		if row.ReadingState == "ready" {
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
				if epoch != a.epoch || request != s.titleRequest || revision != a.readingRevisions[i.key()] || ctx.Err() != nil {
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

func clearUnavailableItem(item *Item) {
	item.Body = ""
	item.BodyLoaded = false
	item.Detail = nil
	item.Preview = ""
	item.Subagents = nil
	item.RecallStats = nil
	item.ReadingState = "unavailable"
	item.ReadingError = "内容不可用"
	if item.Document != nil {
		item.Title = item.Document.title()
	}
}

func (a *App) allReadingPanes() []*pane {
	seen := make(map[*pane]bool, len(a.panes)+len(a.access.Queues))
	out := make([]*pane, 0, len(a.panes)+len(a.access.Queues))
	for _, s := range a.panes {
		if s != nil && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	for _, q := range a.access.Queues {
		if q != nil && q.Reader != nil && !seen[q.Reader] {
			seen[q.Reader] = true
			out = append(out, q.Reader)
		}
	}
	return out
}

func (a *App) clearUserProfileBody(dataset, id string) {
	s := &a.userProfiles
	match := dataset == ""
	if !match && id != "" {
		if strings.HasSuffix(s.BodyKey, "/"+dataset+"/"+id) {
			match = true
		}
		if p, ok := a.selectedUserProfile(); ok && p.Document != nil && p.Document.Dataset == dataset && p.Document.ID == id {
			match = true
		}
	} else if !match {
		if s.BodyKey == "" || strings.Contains(s.BodyKey, "/"+dataset+"/") {
			match = true
		}
		if p, ok := a.selectedUserProfile(); ok && p.Document != nil && p.Document.Dataset == dataset {
			match = true
		}
	}
	if match {
		s.Body = ""
		s.BodyKey = ""
		s.ReadAt = time.Time{}
		s.Reading = false
		s.ReadGeneration++
	}
}

func (a *App) unavailableReadingItem(i Item) {
	a.projectUI.InfoOpen = false
	for n, r := range a.searchUI.Rows {
		if r.Dataset == i.Dataset && (r.ID == i.ID || r.SourceDocument == i.ID) {
			a.searchUI.Rows[n].Body = ""
			a.searchUI.Rows[n].BodyLoaded = false
			a.searchUI.Rows[n].Detail = nil
			a.searchUI.Rows[n].Preview = ""
			a.searchUI.Rows[n].Subagents = nil
			a.searchUI.Rows[n].Title = "内容已失效"
			a.searchUI.Rows[n].ReadingState = "unavailable"
		}
	}
	if a.searchAnchor != nil && a.searchAnchor.Dataset == i.Dataset && (a.searchAnchor.ID == i.ID || a.searchAnchor.SourceDocument == i.ID) {
		a.clearSearchOrigin()
	}
	a.clearUserProfileBody(i.Dataset, i.ID)
	if a.graph != nil && a.graph.dataset == i.Dataset {
		a.graph.clear()
		a.graphSelection = ""
		a.graphLoading = false
	} else if a.graphSelection != "" && (strings.Contains(a.graphSelection, i.ID) || (i.Dataset != "" && strings.Contains(a.graphSelection, i.Dataset))) {
		a.graphSelection = ""
	}
	a.titles.InvalidateObject(i.Dataset, i.ID)
	a.detailChunks.InvalidateObject(i.Dataset, i.ID)
	if a.readingRevisions == nil {
		a.readingRevisions = map[string]uint64{}
	}
	a.readingRevisions[i.key()]++
	for _, s := range a.allReadingPanes() {
		if s.DetailLists != nil {
			k := i.key()
			delete(s.DetailLists, k)
			delete(s.DetailLists, k+"/tools")
			delete(s.DetailLists, k+"/subagents")
		}
		if s.SourcePage != nil && s.SourcePage.ID == i.ID && s.SourcePage.Dataset == i.Dataset {
			s.SourcePage = nil
		}
		if s.SubagentParent != nil && s.SubagentParent.key() == i.key() {
			s.SubagentParent = nil
			s.SubagentParentPage = nil
		} else if s.SubagentParent != nil {
			for idx := range s.SubagentParent.Subagents {
				if s.SubagentParent.Subagents[idx].key() == i.key() {
					clearUnavailableItem(&s.SubagentParent.Subagents[idx])
				}
			}
		}
		if s.SubagentParentPage != nil && s.SubagentParentPage.ID == i.ID && s.SubagentParentPage.Dataset == i.Dataset {
			s.SubagentParentPage = nil
		}
		if s.HomeAnchor != nil && s.HomeAnchor.key() == i.key() {
			s.HomeAnchor = nil
		}
		if s.Selected != nil && s.Selected.key() == i.key() {
			s.Selected = nil
			s.Reading = false
			s.SessionSubagents = false
		}
		history := s.History[:0]
		for _, h := range s.History {
			if h.key() != i.key() {
				history = append(history, h)
			}
		}
		clear(s.History[len(history):])
		s.History = history
		for n, row := range s.Items {
			if row.key() == i.key() {
			}
		}
		for n, i := range s.Knowledge.Catalog {
			if dataset == "" || i.Dataset == dataset {
				clearUnavailableItem(&s.Knowledge.Catalog[n])
