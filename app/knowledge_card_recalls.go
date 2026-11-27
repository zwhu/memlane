package app

import (
	"context"
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"
)

func knowledgeCardRecall(i Item) (string, string) {
	if i.RecallStats == nil {
		return "命中 —", "已记录的检索命中次数尚未读到，请稍后重试。"
	}
	tip := "已记录的检索命中次数（仅统计已落账的 Agent 检索命中，人工阅读不计入；统计写入失败不阻断召回）。"
	if i.RecallStats.StartedAt != "" {
		tip += "自 " + displayDate(i.RecallStats.StartedAt) + " 起记录，旧历史不补算。"
	}
	if i.RecallStats.LastRecalledAt != "" {
		tip += " 最近命中：" + displayDate(i.RecallStats.LastRecalledAt)
	}
	return fmt.Sprintf("命中 %d", i.RecallStats.Count), tip
}

func knowledgeRecallBadge(c *ui.Context, i Item) {
	v4RecallChip(c, i)
}

// Read the existing receipts endpoint, never run retrieval or model inference.
// Only displayed cards are requested; four short requests may run together.
func (a *App) loadKnowledgeCardRecalls(s *pane) {
	if s.Knowledge.V2Mode {
		return
	}
	if len(s.Knowledge.Catalog) > 0 {
		a.loadKnowledgeCatalogRecalls(s)
		return
	}
	client, ok := a.accessClient()
	if !ok || a.Update == nil || s.Loading || !s.Loaded || s.Knowledge.RecallPending != 0 {
		return
	}
	if s.Knowledge.RecallChecked == nil || time.Now().After(s.Knowledge.RecallNext) {
		s.Knowledge.RecallChecked = map[string]bool{}
		s.Knowledge.RecallNext = time.Now().Add(30 * time.Second)
	}
	rows := []Item{}
	for _, i := range s.Items {
		if i.ReadingState != "unavailable" && !s.Knowledge.RecallChecked[i.Key()] {
			rows = append(rows, i)
			s.Knowledge.RecallChecked[i.Key()] = true
		}
	}
	if len(rows) == 0 {
		return
	}
	s.Knowledge.RecallRequest++
	request, epoch := s.Knowledge.RecallRequest, a.epoch
	s.Knowledge.RecallPending = len(rows)
	parent := s.titleCtx
	if parent == nil {
		parent = context.Background()
	}
	jobs := make(chan Item, len(rows))
	for _, i := range rows {
		jobs <- i
	}
	close(jobs)
	for worker := 0; worker < min(4, len(rows)); worker++ {
		go func() {
			for i := range jobs {
				ctx, cancel := context.WithTimeout(parent, 2*time.Second)
				stats, _ := client.knowledgeRecall(ctx, i)
				cancel()
				a.Update(func() {
					if epoch != a.epoch || request != s.Knowledge.RecallRequest {
						return
					}
					s.Knowledge.RecallPending--
					if !a.syncReadingIdentity() {
						return
					}
					for n, row := range s.Items {
						if row.Key() == i.Key() {
							if row.ReadingState == "unavailable" {
								s.Items[n].RecallStats = nil
								return
							}
							s.Items[n].RecallStats = stats
							return
						}
					}
				})
			}
		}()
	}
}

func (a *App) loadKnowledgeCatalogRecalls(s *pane) {
	client, ok := a.accessClient()
	p, hasProject := a.current()
	if !ok || !hasProject || a.Update == nil || s.Loading || !s.Loaded || s.Knowledge.RecallPending != 0 || time.Now().Before(s.Knowledge.RecallNext) {
		return
	}
	s.Knowledge.RecallNext = time.Now().Add(30 * time.Second)
	s.Knowledge.RecallRequest++
	request, epoch := s.Knowledge.RecallRequest, a.epoch
	s.Knowledge.RecallPending = 1
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		counts, err := client.knowledgeRecallCounts(ctx, p.Dataset)
		a.Update(func() {
			if epoch != a.epoch || request != s.Knowledge.RecallRequest || !a.syncReadingIdentity() {
				return
			}
			s.Knowledge.RecallPending = 0
			if err != nil {
				s.Knowledge.CountsError = err.Error()
				s.Knowledge.CountsLoaded = false
				return
			}
			s.Knowledge.CountsLoaded = true
			s.Knowledge.CountsError = ""
			applyRecallCounts(s.Knowledge.Catalog, counts)
			applyRecallCounts(s.Items, counts)
			if s.Knowledge.SortByRecalls {
				if s.ListScroll.Y <= 0 {
					a.reorderKnowledgeRange(s, max(knowledgeBatch, len(s.Items)), false)
				} else {
					s.Knowledge.RecallOrderPending = true
				}
			}
		})
	}()
}

// Batch existing receipts by dataset; never run retrieval or model inference.
func (a *App) loadOverviewRecallStats(generation uint64) {
	client, ok := a.accessClient()
	if !ok || a.Update == nil {
		return
	}
	if a.overviewState.RecallCancel != nil {
		a.overviewState.RecallCancel()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	a.overviewState.RecallCancel = cancel
	epoch, identity := a.epoch, a.readingIdentity
	groups := map[string][]Item{}
	for _, row := range a.overviewState.Snapshot.Knowledge {
		if row.Dataset != "" {
			groups[row.Dataset] = append(groups[row.Dataset], row)
		}
	}
	jobs := make(chan string, len(groups))
	for dataset := range groups {
		jobs <- dataset
	}
	close(jobs)
	for worker := 0; worker < min(4, len(groups)); worker++ {
		go func() {
			for dataset := range jobs {
				if ctx.Err() != nil {
					return
				}
				counts, err := client.knowledgeRecallCounts(ctx, dataset)
				rows := groups[dataset]
				if err == nil {
					applyRecallCounts(rows, counts)
				}
				stats := map[string]*KnowledgeRecallStats{}
				for _, row := range rows {
					if err == nil {
						stats[row.Key()] = row.RecallStats
					}
				}
				if ctx.Err() != nil {
					return
				}
				a.Update(func() {
					if ctx.Err() != nil || generation != a.overviewState.Generation || epoch != a.epoch || identity != a.readingIdentity || !a.syncReadingIdentity() {
						return
					}
					for n, row := range a.overviewState.Snapshot.Knowledge {
						if row.Dataset == dataset {
							a.overviewState.Snapshot.Knowledge[n].RecallStats = stats[row.Key()]
						}
					}
				})
			}
		}()
	}
}
