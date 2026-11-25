package app

import (
	"strings"
)

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
	if a.settingsController != nil {
		a.settingsController.ClearProjectQueues(dataset)
	}
}
