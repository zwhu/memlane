package app

import (
	"context"
	"fmt"
	"time"

	"github.com/zwhu/memlane/app/settings"
)

func (a *App) load(s *pane) {
	a.syncReadingIdentity()
	if a.page == "知识" && a.kind == "知识" && s.Knowledge.Initialized && s.HomeAnchor == nil && s.Query == "" {
		a.loadKnowledgeFeed(s)
		return
	}
	if s.HomeAnchor != nil && len(s.History) == 0 {
		if a.page == "通用知识" && s.HomeCategory != a.commonCategoryFilter() {
			s.HomeAnchor = nil
		}
	}
	if s.HomeAnchor != nil && len(s.History) == 0 {
		if s.Offset == s.HomeOffset && (s.Selected == nil || s.Selected.Key() == s.HomeAnchor.Key()) {
			a.locateOverviewItem(s, *s.HomeAnchor)
			return
		}
		s.HomeAnchor = nil
	}
	a.cancelTitles(s)
	if s.cancel != nil {
		s.cancel()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	s.cancel = cancel
	s.request++
	request, epoch, authEpoch := s.request, a.epoch, a.authEpoch
	refreshing := s.Loaded
	s.Loading = true
	s.Error = ""
	p, ok := a.current()
	page, kind, offset, q := a.page, a.kind, s.Offset, s.Query
	rawFeed := page == "知识" && (kind == "原始记录" || kind == "会话" || kind == "文档") && q == ""
	category := a.commonCategoryFilter()
	commonPageIdx := s.CommonPageIndex
	var commonOpts v2KnowledgePageOpts
	if commonPageIdx >= 0 && commonPageIdx < len(s.CommonNav) {
		commonOpts = s.CommonNav[commonPageIdx]
	} else if commonPageIdx > 0 && commonPageIdx < len(s.CommonCursors) {
		commonOpts = v2KnowledgePageOpts{Cursor: s.CommonCursors[commonPageIdx]}
	}
	commonOpts.RecallAgentID, commonOpts.RecallEventID = s.Knowledge.RecallAgentID, s.Knowledge.RecallEventID
	recallStartedAt := a.overviewState.V2RecallStartedAt
	filter := "原始材料"
	if kind == "知识" {
		filter = "知识"
	}
	client := a.Client
	control, hasControl := a.accessClient()
	go func() {
		defer cancel()
		var rows []Item
		var rawCacheReady bool
		var usedV2Raw bool
		var v2RawNextCursor string
		var usedV2Common bool
		var v2CommonNextCursor string
		var v2CommonPrevCursor string
		var v2CommonHasPrev bool
		var v2CommonTotal int
		var v2CommonCursorReset bool
		var more bool
		var err error
		if q != "" {
			rows, err = client.search(ctx, []Project{p}, q, filter)
			if page == "通用知识" {
				filtered := []Item{}
				for _, row := range rows {
					if row.CommonCategory != "" && (category == "" || row.CommonCategory == category) {
						filtered = append(filtered, row)
					}
				}
				rows = filtered
			}
		} else if !ok {
			rows = []Item{}
		} else if p.Dataset == "" {
			err = fmt.Errorf("项目正在登记，内容归属尚未确认")
		} else if page == "通用知识" {
			if hasControl {
				usedV2Common = true
				v2Resp, v2Err := control.commonPage(ctx, p.Dataset, category, 30, commonOpts)
				if v2Err != nil && isControlInvalidCursor(v2Err) && (commonOpts.Cursor != "" || commonOpts.AnchorDocID != "") {
					// Explicitly reset expired cursor to page 1 under the same filter
					// instead of silently falling back to legacy offset pagination.
					v2CommonCursorReset = true
					v2Resp, v2Err = control.commonPage(ctx, p.Dataset, category, 30, v2KnowledgePageOpts{
						RecallAgentID: commonOpts.RecallAgentID, RecallEventID: commonOpts.RecallEventID,
					})
				}
				if v2Err == nil {
					v2CommonNextCursor = v2Resp.NextCursor
					v2CommonPrevCursor = v2Resp.PrevCursor
					v2CommonHasPrev = v2Resp.HasPrev
					v2CommonTotal = v2Resp.TotalFiltered
					more = v2Resp.HasMore
					rows = make([]Item, 0, len(v2Resp.Items))
					for _, itemRow := range v2Resp.Items {
						if itemRow.DocID == "" {
							continue
						}
						rows = append(rows, itemRow.toItem(recallStartedAt))
					}
				} else {
					err = v2Err
				}
			} else {
				err = fmt.Errorf("控制面服务不可用，无法加载通用知识")
			}
		} else if page == "知识" && (kind == "原始记录" || kind == "会话" || kind == "文档") {
			if hasControl {
				usedV2Raw = true
				resp, readErr := control.rawPage(ctx, p.ID, kind, "")
				if readErr != nil {
					err = readErr
				} else {
					rows, err = v2RawRecordsToItems(p.Dataset, resp)
					more = resp.HasMore
					rawCacheReady = resp.CacheReady
					v2RawNextCursor = resp.NextCursor
				}
			} else {
				err = fmt.Errorf("控制面服务不可用，无法加载原始记录")
			}
		} else if kind == "会话" {
			rows, more, err = client.sessions(ctx, p, offset)
		} else {
			rows, more, err = client.documents(ctx, p, offset, kind == "文档")
		}
		a.Update(func() {
			if a.epoch != epoch || a.authEpoch != authEpoch || s.request != request {
				return
			}
			s.Loading = false
			if err != nil {
				s.Error = settings.ControlUserError(err)
				if readingUnavailable(err) {
					a.InvalidateReading(p.Dataset)
				}
				return
			}
			if a.searchPane == s && a.searchAnchor != nil && !containsReadingItem(rows, a.searchAnchor.Key()) {
				rows = append([]Item{*a.searchAnchor}, rows...)
			}
			if s.SourceReturn != nil && sourceSession(s) && !containsReadingItem(rows, s.Selected.Key()) {
				rows = append([]Item{*s.Selected}, rows...)
			}
			if refreshing && !rawCacheReady && !usedV2Common {
				preserveResolvedReadingTitles(rows, s.Items)
			}
			a.withdrawChangedReadingBody(s, rows)
			s.Items = rows
			if page == "通用知识" {
				s.CommonV2Mode = usedV2Common
				if usedV2Common {
					if v2CommonCursorReset || len(s.CommonNav) == 0 {
						s.CommonNav = []v2KnowledgePageOpts{{}}
						s.CommonCursors = []string{""}
						s.CommonPageIndex = 0
						s.Offset = 0
					}
					s.CommonNextCursor = v2CommonNextCursor
					s.CommonPrevCursor = v2CommonPrevCursor
					s.CommonHasPrev = v2CommonHasPrev
					s.CommonTotalFiltered = v2CommonTotal
				} else {
					s.CommonNav = nil
					s.CommonCursors = nil
					s.CommonPageIndex = 0
					s.CommonNextCursor = ""
					s.CommonPrevCursor = ""
					s.CommonHasPrev = false
					s.CommonTotalFiltered = 0
				}
			}
			if rawFeed {
				s.RawV2Mode = usedV2Raw
				s.RawNextCursor = v2RawNextCursor
				s.RawAppending = false
				s.RawCatalog = nil
				s.Offset = 0
				if !refreshing && len(rows) > 0 && a.page == "知识" && a.kind == kind {
					a.motionRuns.rawPage++
					a.motionRuns.rawList++
				}
			}
			s.More = more
			s.Loaded = true
			if !usedV2Common {
				a.startTitles(s)
			}
			if len(s.History) > 0 {
				return
			}
			if s.Selected != nil {
				targetKey := s.Selected.Key()
				if s.SubagentParent != nil {
					targetKey = s.SubagentParent.Key()
				}
				for _, i := range rows {
					if i.Key() == targetKey {
						if refreshing && s.SubagentParent == nil && (!s.Selected.BodyLoaded || s.Selected.Session != nil) {
							a.selectReadingItem(s, i, false)
						}
						return
					}
				}
				if q == "" && (!refreshing || !s.Selected.BodyLoaded) {
					s.Selected = nil
					s.SubagentParent, s.SubagentParentPage = nil, nil
					s.SessionSubagents = false
					s.Error = "选中内容已不在当前官方页面，请重新选择"
				}
			} else if rawFeed && len(s.Items) > 0 && !s.Locating && usedV2Raw {
				a.selectReadingItem(s, s.Items[0], false)
			}
		})
	}()
}
