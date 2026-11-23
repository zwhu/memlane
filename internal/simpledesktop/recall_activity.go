package simpledesktop

import (
	"context"
	"fmt"
	"sort"
	"strconv"

	"github.com/egoist/mygo/ui"
)

type RecallHit struct {
	Dataset  string `json:"dataset_id"`
	Document string `json:"document_id"`
}
type RecallEvent struct {
	ID        string      `json:"event_id"`
	AgentID   string      `json:"agent_id"`
	AgentName string      `json:"agent_name"`
	AgentKind string      `json:"agent_kind"`
	Channel   string      `json:"channel"`
	Received  string      `json:"received_at"`
	Hits      []RecallHit `json:"hits"`
}

func (c ProjectControlClient) recallEvents(ctx context.Context, limit int) ([]RecallEvent, bool, error) {
	var page struct {
		Items []RecallEvent `json:"items"`
		More  bool          `json:"more"`
	}
	err := c.request(ctx, "GET", "/v1/knowledge/recall-events?limit="+strconv.Itoa(min(100, limit)), nil, &page)
	return page.Items, page.More, err
}

// Successful raw pipeline calls do not prove a document was delivered. Once
// receipt history is available, use those exact identifiers for recall events.
func mergeRecallActivity(events []overviewEvent, recalls []RecallEvent, projects []Project, want int) ([]overviewEvent, bool) {
	merged := []overviewEvent{}
	for _, event := range events {
		if event.Record.Operation == "recall" && event.State == "已完成" {
			continue
		}
		merged = append(merged, event)
	}
	names := map[string]string{}
	for _, p := range projects {
		names[p.Dataset] = p.Name
	}
	for _, recall := range recalls {
		if len(recall.Hits) == 0 {
			continue
		}
		name := agentDisplayName(recall.AgentName)
		if label, ok := map[string]string{"codex": "Codex", "pi": "pi", "traex": "TRAE CLI", "trae_ide": "TRAE IDE", "claude_code": "Claude Code", "cursor": "Cursor", "opencode": "OpenCode"}[recall.AgentKind]; ok {
			name = label
		}
		title := name + " 召回记忆"
		if recall.Channel == "automatic" {
			title = name + " 自动召回"
		}
		at, ok := officialTime(recall.Received)
		if !ok {
			continue
		}
		project := "多个记忆库"
		if first := recall.Hits[0].Dataset; first != "" {
			same := true
			for _, hit := range recall.Hits {
				if hit.Dataset != first {
					same = false
				}
			}
			if same && names[first] != "" {
				project = names[first]
			}
		}
		copy := recall
		merged = append(merged, overviewEvent{Key: "recall/" + recall.AgentID + "/" + recall.ID, Title: title, State: "已完成", Project: project, At: at, Recall: &copy})
	}
	sort.SliceStable(merged, func(i, j int) bool {
		if merged[i].At.Equal(merged[j].At) {
			return merged[i].Key < merged[j].Key
		}
		return merged[i].At.After(merged[j].At)
	})
	more := len(merged) > want
	return merged[:min(want, len(merged))], more
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
		s.Offset = 0
		s.Selected = nil
		a.load(s)
		return
	}
}

type recallActivityGroup struct {
	dataset, name string
	count         int
}

func (a *App) recallActivityGroups(event RecallEvent) []recallActivityGroup {
	groups := []recallActivityGroup{}
	appendGroup := func(p Project) {
		count := 0
		for _, hit := range event.Hits {
			if hit.Dataset == p.Dataset {
				count++
			}
		}
		if count > 0 {
			groups = append(groups, recallActivityGroup{p.Dataset, p.Name, count})
		}
	}
	for _, p := range a.Config.Projects {
		appendGroup(p)
	}
	for _, p := range a.Config.Common {
		appendGroup(p)
	}
	return groups
}

func (a *App) recallActivityAction(c *ui.Context, event RecallEvent) {
	groups := a.recallActivityGroups(event)
	if len(groups) == 1 {
		b := textButton(c, fmt.Sprintf("查看 %d 条记忆", groups[0].count), func() { a.openRecalledCards(event, groups[0].dataset) }).AlignSelf(ui.Start).FontSize(fsCaption).Padding(0, 2)
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
			if m.Item(fmt.Sprintf("%s · %d 条", g.name, g.count)).Chosen() {
				a.openRecalledCards(event, g.dataset)
			}
		}
	})
}
