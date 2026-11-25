package app

import (
	"context"
	"strings"
	"time"
)

func (a *App) hydrateKnowledgeCards(s *pane) {
	if a.Update == nil {
		return
	}
	if s.titleCtx == nil || s.titleCtx.Err() != nil {
		s.titleCtx, s.titleCancel = context.WithCancel(context.Background())
	}
	ctx, epoch, authEpoch, request, generation := s.titleCtx, a.epoch, a.authEpoch, s.titleRequest, a.readingGeneration(s)
	client := a.Client
	v2Mode := s.Knowledge.V2Mode

	scheduleIndex := func(n int) bool {
		row := s.Items[n]
		if row.BodyLoaded || row.Preview != "" || row.ReadingState == "ready" || row.ReadingState == "pending" || row.ReadingState == "failed" || row.ReadingState == "unavailable" {
			return false
		}
		i, revision := row, a.readingRevisions[row.Key()]
		s.Items[n].ReadingState = "pending"
		go func() {
			result, err := a.titles.read(ctx, client, i, generation, false, true)
			a.Update(func() {
				if ctx.Err() != nil || epoch != a.epoch || authEpoch != a.authEpoch || request != s.titleRequest || revision != a.readingRevisions[i.Key()] || !a.syncReadingIdentity() {
					return
				}
				if err != nil {
					a.handleReadingFailure(i, err)
				}
				for idx, cur := range s.Items {
					if cur.Key() == i.Key() {
						if err != nil {
							s.Items[idx].ReadingState = "failed"
							if readingUnavailable(err) {
								s.Items[idx].ReadingState = "unavailable"
							}
							return
						}
						if v2Mode {
							// Keep full bodies out of list rows in v2 mode; bounded ReadingTitles
							// cache and s.Selected own full document bodies.
							visible := strings.TrimSpace(itemPresentation(result).VisibleBody)
							s.Items[idx].Preview = firstReadingSentenceLimit(visible, 240)
							s.Items[idx].ReadingState = "ready"
							if s.Items[idx].Title == "" {
								s.Items[idx].Title = result.Title
							}
						} else {
							s.Items[idx].Body, s.Items[idx].BodyLoaded, s.Items[idx].BodyTruncated, s.Items[idx].ReadingState = result.Body, true, result.BodyTruncated, "ready"
						}
						if s.Selected != nil && s.Selected.Key() == i.Key() && !s.Selected.BodyLoaded {
							updated := result
							s.Selected = &updated
						}
						return
					}
				}
			})
		}()
		return true
	}

	start, end := titlePreheatRange(s)
	scheduled := 0
	for n := start; n < end && scheduled < maxTitlePreheatWindow; n++ {
		if scheduleIndex(n) {
			scheduled++
		}
	}
	if !v2Mode && scheduled < maxTitlePreheatWindow {
		for n := 0; n < len(s.Items) && scheduled < maxTitlePreheatWindow; n++ {
			if n >= start && n < end {
				continue
			}
			if scheduleIndex(n) {
				scheduled++
			}
		}
	}
}

func knowledgeCardBody(i Item) string {
	if !i.BodyLoaded {
		return ""
	}
	return strings.TrimSpace(itemPresentation(i).VisibleBody)
}

func knowledgeDateLabel(i Item) string {
	date := i.Date
	if i.Document != nil {
		if day, ok := documentKnowledgeDay(*i.Document, time.Local); ok {
			date = day
		} else {
			date = ""
		}
	}
	if date == "" {
		return "来源日期未知"
	}
	if t, ok := officialTime(date); ok {
		return t.In(time.Local).Format("2006-01-02")
	}
	return date
}

// knowledgePage renders the V4 Three-Pane project knowledge view:
// 360px list column (underline tabs + filter toolbar + keyboard-navigable list)
// beside the full reading pane.
