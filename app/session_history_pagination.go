package app

import (
	"context"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/app/reading"
	"github.com/zwhu/memlane/app/settings"
)

func (a *App) loadMoreSubagents(s *pane, parent Item) {
	if s == nil || parent.Session == nil || parent.SubagentsLoading || a.Update == nil {
		return
	}
	p, ok := a.current()
	control, hasControl := a.accessClient()
	if !ok || !hasControl || p.ID == "" {
		return
	}
	if s.Selected != nil && s.Selected.Key() == parent.Key() {
		s.Selected.SubagentsLoading = true
	}
	for idx := range s.Items {
		if s.Items[idx].Key() == parent.Key() {
			s.Items[idx].SubagentsLoading = true
		}
	}
	epoch, authEpoch, request := a.epoch, a.authEpoch, s.readRequest
	cursor := parent.SubagentsNextCursor
	parentTitle := sessionReadableParentTitle(parent)
	userID := parent.Session.User
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		resp, err := control.rawChildren(ctx, p.ID, p.Dataset, parent.ID, userID, cursor)
		reloaded := cursor != "" && isControlInvalidCursor(err)
		if reloaded {
			resp, err = control.rawChildren(ctx, p.ID, p.Dataset, parent.ID, userID, "")
		}
		a.Update(func() {
			if epoch != a.epoch || authEpoch != a.authEpoch || request != s.readRequest || !a.syncReadingIdentity() {
				return
			}
			applyTarget := func(target *Item) {
				if target == nil {
					return
				}
				target.SubagentsLoading = false
				if err != nil {
					if readingUnavailable(err) {
						target.Subagents = nil
						target.SubagentsHasMore = false
						target.SubagentsNextCursor = ""
						target.SubagentTotalCount = 0
					} else if isControlInvalidCursor(err) {
						target.SubagentsNextCursor = ""
					}
					return
				}
				if reloaded {
					target.Subagents = nil
				}
				children := make([]Item, 0, len(resp.Items))
				for _, entry := range resp.Items {
					if entry.ID == "" {
						continue
					}
					child := rawSessionEntryToItem(parent.Dataset, entry, true)
					child.ParentTitle = parentTitle
					children = append(children, child)
				}
				target.Subagents = reading.AppendDistinct(target.Subagents, children, Item.Key)
				target.SubagentTotalCount = max(target.SubagentTotalCount, resp.TotalCount, len(target.Subagents))
				target.Subagents, _ = reading.EnforceSlidingWindowDir(target.Subagents, true)
				target.SubagentsHasMore = resp.HasMore
				target.SubagentsNextCursor = resp.NextCursor
			}
			if err != nil {
				s.Error = settings.ControlUserError(err)
			}
			if s.Selected != nil && s.Selected.Key() == parent.Key() {
				applyTarget(s.Selected)
			}
			for idx := range s.Items {
				if s.Items[idx].Key() == parent.Key() {
					applyTarget(&s.Items[idx])
				}
			}
			if err != nil && readingUnavailable(err) {
				a.handleReadingFailure(parent, err)
			}
		})
	}()
}

func (a *App) loadMoreSession(s *pane) {
	p := s.SourcePage
	if p == nil || s.Reading {
		return
	}
	qa, trace := 0, 0
	if s.SessionTools {
		if !p.TraceMore {
			return
		}
		trace = len(p.Traces)
	} else {
		if !p.QAMore {
			return
		}
		qa = len(p.QAs)
	}
	a.readSessionBatch(s, qa, trace, true)
}

func (a *App) sessionLoadMore(c *ui.Context, s *pane, item Item) {
	p := s.SourcePage
	if p == nil {
		return
	}
	name, more := "问答", p.QAMore
	key := item.Key()
	if s.SessionTools {
		name, more = "工具记录", p.TraceMore
		key += "/tools"
	}
	if !more {
		homeText(c, "已加载全部"+name, homeMeta)
		return
	}
	scroll := s.DetailScroll[key]
	a.checkSessionAutoLoad(s, item, scroll)
	if s.Reading {
		homeText(c, "正在加载更多"+name+"…", homeMeta)
	} else {
		projectLink(c, "继续加载"+name, func() {
			if scroll != nil {
				s.SessionLoadMaxY = scroll.MaxY
			}
			a.loadMoreSession(s)
		})
	}
}
