package simpledesktop

import (
	"context"
	"fmt"
	"time"
)

func knowledgeCardRecall(i Item) (string, string) {
	if i.RecallStats == nil {
		return "召回 —", "召回统计暂未确认"
	}
	tip := "Agent 召回次数，人工阅读不计入。"
	return fmt.Sprintf("召回 %d 次", i.RecallStats.Count), tip
}

// Read the existing receipts endpoint, never run retrieval or model inference.
// Only displayed cards are requested; four short requests may run together.
func (a *App) loadKnowledgeCardRecalls(s *pane) {
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
		if i.ReadingState != "unavailable" && !s.Knowledge.RecallChecked[i.key()] {
			rows = append(rows, i)
			s.Knowledge.RecallChecked[i.key()] = true
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
						if row.key() == i.key() {
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

// Fetch existing recall receipts for the recent overview rows without blocking its catalog.
func (a *App) loadOverviewRecallStats(generation uint64) {
	client, ok := a.accessClient()
	if !ok || a.Update == nil {
		return
	}
	rows := a.overviewKnowledgeRows(time.Now())
	jobs := make(chan Item, len(rows))
	for _, i := range rows {
		jobs <- i
	}
	close(jobs)
	for worker := 0; worker < min(4, len(rows)); worker++ {
		go func() {
			for i := range jobs {
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				stats, _ := client.knowledgeRecall(ctx, i)
				cancel()
				a.Update(func() {
					if generation != a.overviewState.generation {
						return
					}
					for n, row := range a.overviewState.Snapshot.Knowledge {
						if row.key() == i.key() {
							a.overviewState.Snapshot.Knowledge[n].RecallStats = stats
							break
						}
					}
				})
			}
		}()
	}
}
