package app

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
		revision := a.readingRevisions[i.Key()]
		go func() {
			result, err := a.titles.read(ctx, client, i, generation, false, true)
			a.Update(func() {
				if request != a.recentRequest || epoch != a.epoch || authEpoch != a.authEpoch || revision != a.readingRevisions[i.Key()] || ctx.Err() != nil || !a.syncReadingIdentity() {
					return
				}
				updateSlice := func(target []Item) bool {
					matched := false
					for n, current := range target {
						if current.Key() == i.Key() {
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
	request, epoch, authEpoch, revision := a.recentRequest, a.epoch, a.authEpoch, a.readingRevisions[i.Key()]
	client := a.Client
	generation := fmt.Sprint(a.overviewState.Generation)
	go func() {
		result, err := a.titles.read(ctx, client, i, generation, true, false)
		a.Update(func() {
			if request != a.recentRequest || epoch != a.epoch || authEpoch != a.authEpoch || revision != a.readingRevisions[i.Key()] || ctx.Err() != nil || !a.syncReadingIdentity() {
				return
			}
			a.handleReadingFailure(i, err)
			apply := func(target []Item) {
				for n, row := range target {
					if row.Key() == i.Key() {
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
		revision := a.readingRevisions[i.Key()]
		if s.Items[n].ReadingState != "ready" {
			s.Items[n].ReadingState = "pending"
		}
		go func() {
			result, err := a.titles.read(ctx, client, i, generation, false, true)
			a.Update(func() {
				if epoch != a.epoch || authEpoch != a.authEpoch || request != s.titleRequest || revision != a.readingRevisions[i.Key()] || ctx.Err() != nil {
					return
				}
				if !a.syncReadingIdentity() {
					return
				}
				a.handleReadingFailure(i, err)
				for idx, current := range s.Items {
					if current.Key() == i.Key() {
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
						if s.Selected != nil && s.Selected.Key() == i.Key() && !s.Selected.BodyLoaded {
							s.Selected.Title = title
						}
						return
					}
				}
			})
		}()
	}
}
