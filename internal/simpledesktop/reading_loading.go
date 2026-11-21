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
	var scope strings.Builder
	fmt.Fprintf(&scope, "%d/%d/%s", a.epoch, a.overviewState.generation, a.readingIdentity)
	for _, i := range rows {
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
	request, epoch := a.recentRequest, a.epoch
	client := a.Client
	generation := fmt.Sprint(a.overviewState.generation)
	for _, row := range rows {
		i := row
		if i.Document == nil || readableDocumentTitle(*i.Document) != "" {
			continue
		}
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
func (a *App) startTitles(s *pane) {
	a.cancelTitles(s)
	ctx, cancel := context.WithCancel(context.Background())
	s.titleCancel = cancel
	s.titleCtx = ctx
	request, epoch := s.titleRequest, a.epoch
	generation := a.readingGeneration(s)
	client := a.Client
	for n, row := range s.Items {
		if n >= 30 && !(a.page == "知识" && a.kind != "知识" && s.RawCatalog != nil) {
			break
		}
		if row.ReadingState == "ready" {
			continue
		}
		if row.Session == nil && (row.Kind != "知识" || row.Document == nil || (a.page != "通用知识" && readableDocumentTitle(*row.Document) != "")) {
			continue
		}
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
				s.Items[n].BodyLoaded = false
				s.Items[n].RecallStats = nil
				s.Items[n].ReadingState = "unavailable"
				s.Items[n].ReadingError = "内容不可用"
				if row.Document != nil {
					s.Items[n].Title = row.Document.title()
				}
			}
		}
	}
	for n, row := range a.overviewState.Snapshot.Knowledge {
		if row.key() == i.key() {
			a.overviewState.Snapshot.Knowledge[n].Title = row.Document.title()
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
	request, epoch, generation := s.titleRequest, a.epoch, a.readingGeneration(s)
	client := a.Client
	revision := a.readingRevisions[i.key()]
	go func() {
		defer cancel()
		result, err := a.titles.read(ctx, client, i, generation, true, false)
		a.Update(func() {
			if request != s.titleRequest || epoch != a.epoch || revision != a.readingRevisions[i.key()] || ctx.Err() != nil || !a.syncReadingIdentity() {
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
// revoked. It removes raw data and copy affordances, including nested history.
func (a *App) InvalidateReading(dataset string) {
	a.invalidateUnifiedSearch(dataset)
	a.projectUI.InfoOpen = false
	a.titles.Invalidate(dataset)
	if a.recentCancel != nil {
		a.recentCancel()
		a.recentRequest++
	}
	for n, i := range a.overviewState.Snapshot.Knowledge {
		if dataset == "" || i.Dataset == dataset {
			a.overviewState.Snapshot.Knowledge[n].Body = ""
			a.overviewState.Snapshot.Knowledge[n].BodyLoaded = false
			if i.Document != nil {
				a.overviewState.Snapshot.Knowledge[n].Title = i.Document.title()
			}
		}
	}
	for _, s := range a.panes {
		a.cancelTitles(s)
		if len(s.RawCatalog) > 0 && (dataset == "" || s.RawCatalog[0].Dataset == dataset) {
			s.RawCatalog = nil
			s.More = false
		}
		if s.SourcePage != nil && (dataset == "" || s.SourcePage.Dataset == dataset) {
			s.SourcePage = nil
		}
		if s.Selected != nil && (dataset == "" || s.Selected.Dataset == dataset) {
			if s.readCancel != nil {
				s.readCancel()
			}
			s.readRequest++
			s.Selected = nil
			s.Reading = false
			s.Error = "内容已失效或无权读取，请刷新"
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
				s.Items[n].BodyLoaded = false
				s.Items[n].RecallStats = nil
				s.Items[n].ReadingState = "unavailable"
				s.Items[n].ReadingError = "内容不可用"
				if i.Document != nil {
					s.Items[n].Title = i.Document.title()
				}
			}
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
