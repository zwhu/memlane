package simpledesktop

import (
	"context"
	"fmt"
	"net/url"
	"sort"

	"github.com/egoist/mygo/ui"
)

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

func filteredKnowledgeCatalog(s *pane) []Item {
	rows := s.Knowledge.Catalog
	if len(s.Knowledge.RecallFilter) == 0 {
		return rows
	}
	filtered := []Item{}
	for _, row := range rows {
		if s.Knowledge.RecallFilter[row.ID] {
			filtered = append(filtered, row)
		}
	}
	return filtered
}

func (a *App) reorderKnowledge(s *pane) {
	a.reorderKnowledgeRange(s, knowledgeBatch, true)
}

func (a *App) reorderKnowledgeRange(s *pane, limit int, resetScroll bool) {
	before := map[string]Item{}
	for _, row := range s.Items {
		before[row.key()] = row
	}
	sortKnowledgeRows(s.Knowledge.Catalog, s.Knowledge.SortByRecalls)
	rows := filteredKnowledgeCatalog(s)
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

func (a *App) knowledgeSortBar(c *ui.Context, s *pane) {
	ui.Row(c).FillWidth().Padding(0, knowledgeInset, 8, knowledgeInset).AlignItems(ui.Center).Gap(12).Children(func() {
		if len(s.Knowledge.RecallFilter) > 0 {
			homeText(c, fmt.Sprintf("本次召回 · %d 条", len(s.Knowledge.RecallFilter)), homeBody).FontSize(12).FontWeight(550)
			if s.Loaded && !s.Loading {
				if missing := len(s.Knowledge.RecallFilter) - len(filteredKnowledgeCatalog(s)); missing > 0 {
					homeText(c, fmt.Sprintf("%d 条暂不可用", missing), homeMeta)
				}
			}
			queueAction(c, "显示全部记忆", func() { s.Knowledge.RecallFilter = nil; a.reorderKnowledge(s) })
			queueAction(c, "返回动态", func() { a.navigate("总览", 0) })
		}
		ui.Spacer(c)
		if s.Knowledge.CountsError != "" {
			homeText(c, "召回次数读取失败", homeMeta)
			queueAction(c, "重试", func() { a.loadKnowledgeFeed(s) })
			if s.Knowledge.SortByRecalls {
				queueAction(c, "改按时间", func() { s.Knowledge.SortByRecalls = false; a.reorderKnowledge(s) })
			}
		}
		selected := 0
		if s.Knowledge.SortByRecalls {
			selected = 1
		}
		ui.Segmented(c, &selected, "按时间", "按召回次数").Label("记忆排序").Disabled(!s.Knowledge.CountsLoaded).OnChange(func() {
			s.Knowledge.SortByRecalls = selected == 1
			a.reorderKnowledge(s)
		})
	})
}
