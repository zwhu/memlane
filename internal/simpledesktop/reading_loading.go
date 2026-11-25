package simpledesktop

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/zwhu/memlane/app/reading"
	"github.com/zwhu/memlane/app/shell"
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
	fmt.Fprintf(&scope, "%d/%d/%d/%s", a.epoch, a.authEpoch, a.overviewState.Generation, a.readingIdentity)
	for _, i := range candidates {
		fmt.Fprintf(&scope, "/%v", keyForReading(a.readingIdentity, i, fmt.Sprint(a.overviewState.Generation)))
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
	generation := fmt.Sprint(a.overviewState.Generation)
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
	generation := fmt.Sprint(a.overviewState.Generation)
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
	switch {
	case (ok && status == 401) || strings.Contains(err.Error(), "identity changed") || strings.Contains(err.Error(), "credential unavailable"):
		appShellAdapter{a}.InvalidateReading()
	default:
		a.unavailableReadingItem(i)
	}
}
func (c *readingController) cancelTitles(s *pane) { cancelTitles(s) }

func cancelTitles(s *pane) {
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

func clearUnavailableItem(item *Item) {
	item.Body = ""
	item.BodyLoaded = false
	item.BodyTruncated = false
	item.Detail = nil
	item.Preview = ""
	item.PreviewTruncated = false
	item.Subagents = nil
	item.SubagentsHasMore = false
	item.SubagentsNextCursor = ""
	item.SubagentTotalCount = 0
	item.SubagentsLoading = false
	item.SessionSummary = ""
	item.ParentTitle = ""
	item.RecallStats = nil
	item.ReadingState = "unavailable"
	item.ReadingError = "内容不可用"
	if item.Document != nil {
		doc := *item.Document
		doc.Metadata = nil
		item.Document = &doc
		item.Title = doc.title()
	} else if item.Session != nil {
		item.Title = ""
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
		s.ClearBody()
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
	if a.graphController.Host != nil && a.graphController.Host.Dataset() == i.Dataset {
		a.graphController.Host.Clear()
		a.graphController.Selection = ""
		a.graphController.Loading = false
	} else if a.graphController.Selection != "" && (strings.Contains(a.graphController.Selection, i.ID) || (i.Dataset != "" && strings.Contains(a.graphController.Selection, i.Dataset))) {
		a.graphController.Selection = ""
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
				clearUnavailableItem(&s.Items[n])
			}
		}
		for n, row := range s.RawCatalog {
			if row.key() == i.key() {
				clearUnavailableItem(&s.RawCatalog[n])
			}
		}
		for n, row := range s.Knowledge.Catalog {
			if row.key() == i.key() {
				clearUnavailableItem(&s.Knowledge.Catalog[n])
			}
		}
		if s.Knowledge.Measurements != nil {
			delete(s.Knowledge.Measurements, i.key())
		}
	}
	for _, rows := range [][]Item{a.overviewState.Snapshot.Knowledge, a.overviewState.DayItems} {
		for n, row := range rows {
			if row.key() == i.key() || (row.Session == nil && i.Session != nil &&
				row.Dataset == i.Dataset && row.Kind == i.Kind && row.ID == i.ID) {
				clearUnavailableItem(&rows[n])
			}
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

func (a *App) cancelOverviewRequests() { a.overviewPageController().CancelAll() }

type readingInvalidator interface {
	invalidateReading(dataset string)
}

type readingInvalidatorFunc func(dataset string)

func (f readingInvalidatorFunc) invalidateReading(dataset string) { f(dataset) }

type namedReadingInvalidator struct {
	name        string
	invalidator readingInvalidator
}

// The registry and controller state belong to the UI thread, like authEpoch.
func (a *App) registerReadingInvalidator(name string, inv readingInvalidator) {
	a.ensureReadingInvalidators()
	for i := range a.readingInvalidators {
		if a.readingInvalidators[i].name == name {
			a.readingInvalidators[i].invalidator = inv
			return
		}
	}
	a.readingInvalidators = append(a.readingInvalidators, namedReadingInvalidator{name, inv})
}

func (a *App) ensureReadingInvalidators() []namedReadingInvalidator {
	if a.readingInvalidators == nil {
		a.overviewPageController()
		a.readingInvalidators = []namedReadingInvalidator{
			{"common-editor", readingInvalidatorFunc(a.withdrawCommonEditor)},
			{"search", &a.searchController},
			{"project-ui", readingInvalidatorFunc(func(string) { a.projectUI.InfoOpen = false })},
			{"user-profiles", readingInvalidatorFunc(func(dataset string) { a.clearUserProfileBody(dataset, "") })},
			{"graph", &a.graphController},
			{"reading", &a.readingController},
			{"overview", &a.overviewController},
			{"project-queues", readingInvalidatorFunc(a.invalidateProjectQueues)},
		}
	}
	return a.readingInvalidators
}

// InvalidateReading must be called on the UI thread when access is revoked.
// It withdraws cached content and fences in-flight callbacks before any reset.
func (a *App) InvalidateReading(dataset string) {
	a.authEpoch++
	a.unreadAuthEpoch.Store(a.authEpoch)
	for _, entry := range a.ensureReadingInvalidators() {
		entry.invalidator.invalidateReading(dataset)
	}
}

func (c *overviewController) invalidateReading(dataset string) {
	c.overview.Invalidate(dataset)
}

func invalidateReadingPane(s *pane, dataset string) {
	cancelTitles(s)
	// authEpoch is incremented globally by InvalidateReading, so every in-flight
	// pane load/read callback will be fenced on return; always cancel and clear
	// transient loading flags so empty or unrelated panes never stay stuck loading.
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	s.Loading = false
	s.Locating = false
	s.Knowledge.Appending = false
	s.Knowledge.Prepending = false
	s.RawAppending = false
	paneMatches := dataset == "" ||
		(len(s.Items) > 0 && s.Items[0].Dataset == dataset) ||
		(s.Selected != nil && s.Selected.Dataset == dataset) ||
		(s.HomeAnchor != nil && s.HomeAnchor.Dataset == dataset) ||
		(s.SubagentParent != nil && s.SubagentParent.Dataset == dataset)
	if paneMatches {
		s.request++
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
	if s.DetailLists != nil {
		if dataset == "" {
			clear(s.DetailLists)
		} else {
			prefix := dataset + "/"
			for k := range s.DetailLists {
				if strings.HasPrefix(k, prefix) {
					delete(s.DetailLists, k)
				}
			}
		}
	}
	if len(s.RawCatalog) > 0 {
		if dataset == "" {
			clear(s.RawCatalog)
			s.RawCatalog = nil
			s.More = false
		} else {
			kept := s.RawCatalog[:0]
			for _, row := range s.RawCatalog {
				if row.Dataset != dataset {
					kept = append(kept, row)
				}
			}
			clear(s.RawCatalog[len(kept):])
			if len(kept) == 0 {
				s.RawCatalog = nil
				s.More = false
			} else {
				s.RawCatalog = kept
			}
		}
	}
	if len(s.Knowledge.Catalog) > 0 {
		if dataset == "" {
			clear(s.Knowledge.Catalog)
			s.Knowledge.Catalog = nil
		} else {
			kept := s.Knowledge.Catalog[:0]
			for _, row := range s.Knowledge.Catalog {
				if row.Dataset != dataset {
					kept = append(kept, row)
				}
			}
			clear(s.Knowledge.Catalog[len(kept):])
			if len(kept) == 0 {
				s.Knowledge.Catalog = nil
			} else {
				s.Knowledge.Catalog = kept
			}
		}
	}
	if s.Knowledge.Measurements != nil {
		if dataset == "" {
			clear(s.Knowledge.Measurements)
		} else {
			prefix := dataset + "/"
			for k := range s.Knowledge.Measurements {
				if strings.HasPrefix(k, prefix) {
					delete(s.Knowledge.Measurements, k)
				}
			}
		}
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
	if s.readCancel != nil {
		s.readCancel()
		s.readCancel = nil
	}
	s.Reading = false
	s.ReadingSource = false
	if dataset == "" || (s.Selected != nil && s.Selected.Dataset == dataset) {
		s.readRequest++
		if s.Selected != nil {
			s.Selected = nil
			s.Error = "内容已失效或无权读取，请刷新"
		}
	}
	history := s.History[:0]
	for _, i := range s.History {
		if dataset != "" && i.Dataset != dataset {
			history = append(history, i)
		}
	}
	clear(s.History[len(history):])
	s.History = history
	for n, i := range s.Items {
		if dataset == "" || i.Dataset == dataset {
			clearUnavailableItem(&s.Items[n])
		}
	}
}

func (a *App) invalidateProjectQueues(dataset string) {
	seen := make(map[*pane]bool, len(a.panes)+len(a.access.Queues))
	for _, s := range a.panes {
		seen[s] = true
	}
	for k, q := range a.access.Queues {
		if q == nil {
			continue
		}
		if dataset == "" || strings.HasSuffix(k, "/"+dataset) {
			q.Request++
			q.Loading = false
			q.UnreadState = UnreadState{}
			q.Activity = nil
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

// Poll token changes on the existing view cadence without statting token files in View.
func (a *App) pollViewReadingIdentity(now time.Time) {
	if a.Update == nil || a.Client.Config.TokenFile == "" || now.Before(a.nextViewIdentityCheck) {
		return
	}
	a.nextViewIdentityCheck = now.Add(2 * time.Second)
	client, update := a.Client, a.Update
	adapter := appShellAdapter{app: a}
	fence, activation := shell.CaptureFence(adapter), a.windowActivation
	go func() {
		full, base, err := readingIdentityPair(client)
		if err != nil {
			full = "unavailable"
		}
		if full == fence.ReadingIdentity {
			return
		}
		update(func() {
			if !a.windowHidden && activation == a.windowActivation && fence.Valid(adapter) {
				a.applyReadingIdentity(full, base, err)
			}
		})
	}()
}

func (a *App) syncReadingIdentity() bool {
	return a.applyReadingIdentity(readingIdentityPair(a.Client))
}

func (a *App) applyReadingIdentity(identity, base string, err error) bool {
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

func (a *App) withdrawChangedReadingBody(s *pane, rows []Item) {
	selected := s.Selected
	if selected == nil || selected.Document == nil {
		return
	}
	for _, row := range rows {
		if row.Document == nil || row.key() != selected.key() {
			continue
		}
		old, next := selected.Document, row.Document
		if reading.BodyVersionChanged(old.ContentHash, next.ContentHash, old.Updated, next.Updated) {
			a.unavailableReadingItem(*selected)
			a.selectReadingItem(s, row, false)
		}
		return
	}
}
