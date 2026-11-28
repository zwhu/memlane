package app

import (
	"context"
	"fmt"
	"net/url"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/app/reading"
)

var v4KnowledgeCategories = []string{
	"项目状态", "踩坑经验", "项目约定", "代码位置与关联", "代码变更", "约束条件", "其他",
}

type knowledgeRecallCounts struct {
	StartedAt string `json:"started_at"`
	Items     []struct {
		Document string `json:"document_id"`
		Count    int64  `json:"count"`
		Last     string `json:"last_recalled_at"`
	} `json:"items"`
}

func (c ProjectControlClient) knowledgeRecallCounts(ctx context.Context, dataset string) (*knowledgeRecallCounts, error) {
	var out knowledgeRecallCounts
	if err := c.request(ctx, "GET", "/v1/knowledge/recall-counts?dataset_id="+url.QueryEscape(dataset), nil, &out); err != nil {
		return nil, err
	}
	if len(out.Items) > 0 && out.StartedAt == "" {
		return nil, fmt.Errorf("已记录的检索命中次数尚未确认")
	}
	for _, row := range out.Items {
		if row.Count < 0 || row.Document == "" {
			return nil, fmt.Errorf("已记录的检索命中次数尚未确认")
		}
	}
	return &out, nil
}

func applyRecallCounts(rows []Item, counts *knowledgeRecallCounts) {
	if counts == nil {
		return
	}
	byID := map[string]KnowledgeRecallStats{}
	for _, row := range counts.Items {
		byID[row.Document] = KnowledgeRecallStats{Count: row.Count, LastRecalledAt: row.Last, StartedAt: counts.StartedAt}
	}
	for n := range rows {
		stats := byID[rows[n].ID]
		stats.StartedAt = counts.StartedAt
		rows[n].RecallStats = &stats
	}
}

func sortKnowledgeRows(rows []Item, byRecalls bool) {
	reading.SortByRecalls(rows, byRecalls, Item.Key, func(i Item) string { return i.Date }, func(i Item) int64 {
		if i.RecallStats == nil {
			return -1
		}
		return i.RecallStats.Count
	})
}

func (a *App) knowledgeBaseRows(s *pane) []Item {
	rows := s.Knowledge.Catalog
	if len(rows) == 0 && len(s.Items) > 0 {
		rows = s.Items
	}
	return reading.FilterKnowledge(rows, reading.KnowledgeFilter{
		RecallFilter: s.Knowledge.RecallFilter, UnreadOnly: s.Knowledge.UnreadOnly,
	}, func(i Item) string { return i.ID }, knowledgeCategoryLabel, a.itemIsUnread)
}

func (a *App) filteredKnowledgeCatalog(s *pane) []Item {
	return reading.FilterKnowledge(a.knowledgeBaseRows(s), reading.KnowledgeFilter{CategoryFilter: s.Knowledge.CategoryFilter},
		func(i Item) string { return i.ID }, knowledgeCategoryLabel, a.itemIsUnread)
}

func (a *App) knowledgeCategoryCounts(s *pane) (int, map[string]int) {
	if s.Knowledge.V2Mode {
		counts := s.Knowledge.ServerCategoryCounts
		if counts == nil {
			counts = map[string]int{}
		}
		return s.Knowledge.ServerBaseTotal, counts
	}
	base := a.knowledgeBaseRows(s)
	counts := map[string]int{}
	for _, row := range base {
		if cat := knowledgeCategoryLabel(row); cat != "" {
			counts[cat]++
		}
	}
	return len(base), counts
}

func (a *App) reorderKnowledge(s *pane) {
	a.motionRuns.projectList++
	if s.Knowledge.V2Mode {
		s.ListScroll.Y = 0
		s.Knowledge.NextCursor = ""
		s.Knowledge.RecallOrderPending = false
		s.Knowledge.Columns = map[string]int{}
		a.loadKnowledgeFeed(s)
		return
	}
	a.reorderKnowledgeRange(s, knowledgeBatch, true)
}

func (a *App) reorderKnowledgeRange(s *pane, limit int, resetScroll bool) {
	before := map[string]Item{}
	for _, row := range s.Items {
		before[row.Key()] = row
	}
	sortKnowledgeRows(s.Knowledge.Catalog, s.Knowledge.SortByRecalls)
	rows := a.filteredKnowledgeCatalog(s)
	s.Items = append([]Item(nil), rows[:min(limit, len(rows))]...)
	for n, row := range s.Items {
		if old, ok := before[row.Key()]; ok {
			s.Items[n].Body, s.Items[n].BodyLoaded, s.Items[n].ReadingState = old.Body, old.BodyLoaded, old.ReadingState
		}
	}
	s.More = len(s.Items) < len(rows)
	if resetScroll {
		s.ListScroll.Y = 0
		if len(s.Items) > 0 {
			s.WidgetList.ScrollTo(0, ui.Start)
		}
	}
	s.Knowledge.RecallOrderPending = false
	s.Knowledge.Columns = map[string]int{}
	a.hydrateKnowledgeCards(s)
}

// knowledgeFilterBar renders the V4 single-line filter toolbar (category dropdown
// with counts + unread-only switch + sort dropdown) and optional recall filter bar.
func (a *App) knowledgeFilterBar(c *ui.Context, s *pane) {
	ui.Column(c).FillWidth().Padding(sp3, sp4, sp2, sp4).Gap(sp2).Shrink(0).Children(func() {
		ui.Row(c).FillWidth().MinWidth(0).AlignItems(ui.Center).Gap(sp2).Children(func() {
			ui.Row(c).Grow(1).MinWidth(0).AlignItems(ui.Center).Gap(sp2).Children(func() {
				catValue := s.Knowledge.CategoryFilter
				if catValue == "" {
					catValue = "全部"
				}
				total, counts := a.knowledgeCategoryCounts(s)
				v4DropdownButton(c, "knowledge-category-btn", "记忆分类筛选", "分类", catValue, s.Knowledge.CategoryFilter != "").Shrink(1).MinWidth(0).PopoverMenuButton(func(m *ui.Menu) {
					allLabel := fmt.Sprintf("全部 (%d)", total)
					if s.Knowledge.CategoryFilter == "" {
						allLabel = "✓ " + allLabel
					}
					if m.Item(allLabel).Chosen() {
						s.Knowledge.CategoryFilter = ""
						a.reorderKnowledge(s)
					}
					m.Separator()
					for _, cat := range v4KnowledgeCategories {
						label := fmt.Sprintf("%s (%d)", cat, counts[cat])
						if s.Knowledge.CategoryFilter == cat {
							label = "✓ " + label
						}
						if m.Item(label).Chosen() {
							s.Knowledge.CategoryFilter = cat
							a.reorderKnowledge(s)
						}
					}
				})

				v4Switch(c, "knowledge-unread-sw", &s.Knowledge.UnreadOnly, "仅未读", func() {
					a.reorderKnowledge(s)
				})
			})

			sortLabel := "按时间"
			if s.Knowledge.SortByRecalls {
				sortLabel = "按召回次数"
			}
			v4DropdownButton(c, "knowledge-sort-btn", "记忆排序", "", sortLabel, false).Tooltip("按时间或已记录的检索命中次数排序").PopoverMenuButton(func(m *ui.Menu) {
				timeLabel := "按时间"
				if !s.Knowledge.SortByRecalls {
					timeLabel = "✓ " + timeLabel
				}
				if m.Item(timeLabel).Chosen() {
					s.Knowledge.SortByRecalls = false
					a.reorderKnowledge(s)
				}
				recallLabel := "按召回次数"
				if s.Knowledge.SortByRecalls {
					recallLabel = "✓ " + recallLabel
				}
				if !s.Knowledge.CountsLoaded {
					recallLabel += "（暂不可用）"
				}
				item := m.Item(recallLabel)
				if !s.Knowledge.CountsLoaded {
					item.Disabled(true)
				} else if item.Chosen() {
					s.Knowledge.SortByRecalls = true
					a.reorderKnowledge(s)
				}
			})
		})

		if len(s.Knowledge.RecallFilter) > 0 {
			ui.Row(c).FillWidth().Padding(8, sp2, 8, sp3).Radius(radius).Background(tok.AccentSofter).AlignItems(ui.Center).Gap(sp2).Children(func() {
				ui.Icon(c, v4IconRecall).FontSize(13).TextColor(tok.Accent).Shrink(0)
				ui.Text(c, fmt.Sprintf("本次召回 · %d 条", len(s.Knowledge.RecallFilter))).Font(fontUI).FontSize(fsFootnote).FontWeight(600).TextColor(tok.Accent).SingleLine()
				if s.Loaded && !s.Loading {
					shown := len(a.filteredKnowledgeCatalog(s))
					if s.Knowledge.V2Mode {
						shown = s.Knowledge.TotalFiltered
					}
					if missing := len(s.Knowledge.RecallFilter) - shown; missing > 0 && s.Knowledge.CategoryFilter == "" && !s.Knowledge.UnreadOnly {
						homeText(c, fmt.Sprintf("%d 条暂不可用", missing), homeMeta)
					}
				}
				ui.Spacer(c)
				homeLink(c, "显示全部记忆", func() {
					s.Knowledge.RecallFilter = nil
					s.Knowledge.RecallAgentID = ""
					s.Knowledge.RecallEventID = ""
					a.reorderKnowledge(s)
				})
				homeLink(c, "返回动态", func() { a.navigate("总览", 0) })
			})
		}

		if s.Knowledge.CountsError != "" {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(sp2).Children(func() {
				homeText(c, "已记录的检索命中次数读取失败", homeMeta)
				homeLink(c, "重试", func() { a.loadKnowledgeFeed(s) })
				if s.Knowledge.SortByRecalls {
					homeLink(c, "改按时间", func() { s.Knowledge.SortByRecalls = false; a.reorderKnowledge(s) })
				}
			})
		}
	})
}
