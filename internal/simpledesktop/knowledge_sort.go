package simpledesktop

import (
	"context"
	"fmt"
	"net/url"
	"sort"

	"github.com/egoist/mygo/ui"
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
	if out.StartedAt == "" {
		return nil, fmt.Errorf("召回次数尚未确认")
	}
	for _, row := range out.Items {
		if row.Count < 0 || row.Document == "" {
			return nil, fmt.Errorf("召回次数尚未确认")
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
	sort.SliceStable(rows, func(i, j int) bool {
		if byRecalls {
			x, y := int64(-1), int64(-1)
			if rows[i].RecallStats != nil {
				x = rows[i].RecallStats.Count
			}
			if rows[j].RecallStats != nil {
				y = rows[j].RecallStats.Count
			}
			if x != y {
				return x > y
			}
		}
		x, y := dateOrder(rows[i].Date), dateOrder(rows[j].Date)
		if !x.Equal(y) {
			return x.After(y)
		}
		return rows[i].key() < rows[j].key()
	})
}

func (a *App) knowledgeBaseRows(s *pane) []Item {
	rows := s.Knowledge.Catalog
	if len(rows) == 0 && len(s.Items) > 0 {
		rows = s.Items
	}
	if len(s.Knowledge.RecallFilter) == 0 && !s.Knowledge.UnreadOnly {
		return rows
	}
	filtered := make([]Item, 0, len(rows))
	for _, row := range rows {
		if len(s.Knowledge.RecallFilter) > 0 && !s.Knowledge.RecallFilter[row.ID] {
			continue
		}
		if s.Knowledge.UnreadOnly && !a.itemIsUnread(row) {
			continue
		}
		filtered = append(filtered, row)
	}
	return filtered
}

func (a *App) filteredKnowledgeCatalog(s *pane) []Item {
	rows := a.knowledgeBaseRows(s)
	if s.Knowledge.CategoryFilter == "" {
		return rows
	}
	filtered := make([]Item, 0, len(rows))
	for _, row := range rows {
		if knowledgeCategoryLabel(row) == s.Knowledge.CategoryFilter {
			filtered = append(filtered, row)
		}
	}
	return filtered
}

func (a *App) knowledgeCategoryCounts(s *pane) (int, map[string]int) {
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
	a.reorderKnowledgeRange(s, knowledgeBatch, true)
}

func (a *App) reorderKnowledgeRange(s *pane, limit int, resetScroll bool) {
	before := map[string]Item{}
	for _, row := range s.Items {
		before[row.key()] = row
	}
	sortKnowledgeRows(s.Knowledge.Catalog, s.Knowledge.SortByRecalls)
	rows := a.filteredKnowledgeCatalog(s)
	s.Items = append([]Item(nil), rows[:min(limit, len(rows))]...)
	for n, row := range s.Items {
		if old, ok := before[row.key()]; ok {
			s.Items[n].Body, s.Items[n].BodyLoaded, s.Items[n].ReadingState = old.Body, old.BodyLoaded, old.ReadingState
		}
	}
	s.More = len(s.Items) < len(rows)
	if resetScroll {
		s.ListScroll.Y = 0
	}
	s.Knowledge.RecallOrderPending = false
	s.Knowledge.Columns = map[string]int{}
	a.hydrateKnowledgeCards(s)
}

// knowledgeFilterBar renders the V4 single-line filter toolbar (category dropdown
// with counts + unread-only switch + sort dropdown) and optional recall filter bar.
func (a *App) knowledgeFilterBar(c *ui.Context, s *pane) {
	ui.Column(c).FillWidth().Padding(sp3, sp4, sp2, sp4).Gap(sp2).Shrink(0).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(sp2).Children(func() {
			catValue := s.Knowledge.CategoryFilter
			if catValue == "" {
				catValue = "全部"
			}
			total, counts := a.knowledgeCategoryCounts(s)
			v4DropdownButton(c, "knowledge-category-btn", "记忆分类筛选", "分类", catValue, s.Knowledge.CategoryFilter != "").PopoverMenuButton(func(m *ui.Menu) {
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

			ui.Spacer(c)

			sortLabel := "按时间"
			if s.Knowledge.SortByRecalls {
				sortLabel = "按召回次数"
			}
			v4DropdownButton(c, "knowledge-sort-btn", "记忆排序", "", sortLabel, false).PopoverMenuButton(func(m *ui.Menu) {
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
			ui.Row(c).FillWidth().Padding(6, sp2, 6, sp3).Radius(radius).Background(tok.AccentSofter).AlignItems(ui.Center).Gap(sp2).Children(func() {
				ui.Icon(c, v4IconRecall).FontSize(13).TextColor(tok.Accent).Shrink(0)
				ui.Text(c, fmt.Sprintf("本次召回 · %d 条", len(s.Knowledge.RecallFilter))).Font("system-ui").FontSize(fsFootnote).FontWeight(600).TextColor(tok.Accent).SingleLine()
				if s.Loaded && !s.Loading {
					if missing := len(s.Knowledge.RecallFilter) - len(a.filteredKnowledgeCatalog(s)); missing > 0 && s.Knowledge.CategoryFilter == "" && !s.Knowledge.UnreadOnly {
						homeText(c, fmt.Sprintf("%d 条暂不可用", missing), homeMeta)
					}
				}
				ui.Spacer(c)
				homeLink(c, "显示全部记忆", func() { s.Knowledge.RecallFilter = nil; a.reorderKnowledge(s) })
				homeLink(c, "返回动态", func() { a.navigate("总览", 0) })
			})
		}

		if s.Knowledge.CountsError != "" {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(sp2).Children(func() {
				homeText(c, "召回次数读取失败", homeMeta)
				homeLink(c, "重试", func() { a.loadKnowledgeFeed(s) })
				if s.Knowledge.SortByRecalls {
					homeLink(c, "改按时间", func() { s.Knowledge.SortByRecalls = false; a.reorderKnowledge(s) })
				}
			})
		}
	})
}
