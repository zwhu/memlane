package simpledesktop

import (
	"context"
	"fmt"
	"strconv"

	"github.com/egoist/mygo/ui"
	appoverview "github.com/zwhu/memlane/app/overview"
)

type RecallHit = appoverview.RecallHit
type RecallEvent = appoverview.RecallEvent

func (c ProjectControlClient) recallEvents(ctx context.Context, limit int) ([]RecallEvent, bool, error) {
	var page struct {
		Items []RecallEvent `json:"items"`
		More  bool          `json:"more"`
	}
	err := c.request(ctx, "GET", "/v1/knowledge/recall-events?limit="+strconv.Itoa(min(100, limit)), nil, &page)
	return page.Items, page.More, err
}

func mergeRecallActivity(events []overviewEvent, recalls []RecallEvent, projects []Project, want int) ([]overviewEvent, bool) {
	return appoverview.MergeRecallActivity(events, recalls, overviewProjectRefs(projects), want)
}

func (a *App) openRecalledCards(event RecallEvent, dataset string) {
	filter := map[string]bool{}
	for _, hit := range event.Hits {
		if hit.Dataset == dataset {
			filter[hit.Document] = true
		}
	}
	for index, p := range a.Config.Projects {
		if p.Dataset != dataset {
			continue
		}
		a.navigate("知识", index)
		s := a.state()
		s.Knowledge.RecallFilter = filter
		s.Knowledge.RecallAgentID = event.AgentID
		s.Knowledge.RecallEventID = event.ID
		s.Knowledge.Detail = false
		if !s.Knowledge.V2Mode {
			a.reorderKnowledge(s)
		}
		a.loadKnowledgeFeed(s)
		return
	}
	for index, p := range a.Config.Common {
		if p.Dataset != dataset {
			continue
		}
		a.navigate("通用知识", index)
		s := a.state()
		s.Knowledge.RecallFilter = filter
		s.Knowledge.RecallAgentID, s.Knowledge.RecallEventID = event.AgentID, event.ID
		s.Offset, s.CommonPageIndex = 0, 0
		s.CommonNav, s.CommonCursors = nil, nil
		s.Selected = nil
		a.load(s)
		return
	}
}

func (a *App) recallActivityGroups(event RecallEvent) []appoverview.RecallGroup {
	return appoverview.RecallGroups(event, overviewProjectRefs(append(append([]Project(nil), a.Config.Projects...), a.Config.Common...)))
}

func (a *App) recallActivityAction(c *ui.Context, event RecallEvent) {
	groups := a.recallActivityGroups(event)
	if len(groups) == 1 {
		b := textButton(c, fmt.Sprintf("查看 %d 条记忆", groups[0].Count), func() { a.openRecalledCards(event, groups[0].Dataset) }).AlignSelf(ui.Start).FontSize(fsCaption).Padding(0, 2)
		if !b.Hovered() {
			b.TextColor(tok.Text3)
		}
		return
	}
	if len(groups) == 0 {
		homeText(c, "记忆库暂不可用", homeMeta)
		return
	}
	b := textButton(c, "查看召回记忆", nil).AlignSelf(ui.Start).FontSize(fsCaption).Padding(0, 2)
	if !b.Hovered() {
		b.TextColor(tok.Text3)
	}
	b.PopoverMenuButton(func(m *ui.Menu) {
		for _, g := range groups {
			if m.Item(fmt.Sprintf("%s · %d 条", g.Name, g.Count)).Chosen() {
				a.openRecalledCards(event, g.Dataset)
			}
		}
	})
}
