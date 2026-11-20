package simpledesktop

import "sort"

type generationProfileItem struct {
	ID     string
	Row    GenerationRow
	Title  string
	Detail string
	Icon   string
}
type generationProfileGroup struct {
	Key   string
	Title string
	Items []generationProfileItem
}

// One presentation boundary keeps Agent identity separate from model identity.
// Grouping is a view only: revisions, credentials and configuration IDs remain intact.
func generationProfileGroups(g GenerationDirectory) []generationProfileGroup {
	groups := map[string]*generationProfileGroup{}
	ids := make([]string, 0, len(g.Profiles))
	for id := range g.Profiles {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		rows := g.Profiles[id]
		if len(rows) == 0 {
			continue
		}
		row := rows[len(rows)-1]
		key, title := "agent", "Agent CLI"
		item := generationProfileItem{ID: id, Row: row}
		if row.Profile.Mode == "agent_bridge" {
			item.Icon = row.Profile.BridgeKind
			item.Title = generationBridgeLabel(row.Profile.BridgeKind)
			item.Detail = generationDefaultModel(g, row.Profile.BridgeKind)
		} else {
			key = "provider:" + row.Profile.Provider + ":" + row.Profile.Endpoint
			title = map[string]string{"openai": "OpenAI", "anthropic": "Anthropic", "gemini": "Gemini", "custom": "OpenAI 兼容", "ollama": "Ollama"}[row.Profile.Provider]
			if title == "" {
				title = row.Profile.Provider
			}
			item.Title = row.Profile.Model
		}
		if groups[key] == nil {
			groups[key] = &generationProfileGroup{Key: key, Title: title}
		}
		groups[key].Items = append(groups[key].Items, item)
	}
	// Distinct endpoints must remain distinguishable even for one provider.
	counts := map[string]int{}
	for key, group := range groups {
		if key != "agent" {
			counts[group.Title]++
		}
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]generationProfileGroup, 0, len(keys))
	for _, key := range keys {
		group := groups[key]
		if key != "agent" && counts[group.Title] > 1 {
			group.Title += " · " + group.Items[0].Row.Profile.Endpoint
		}
		result = append(result, *group)
	}
	return result
}

func generationWorkspaceGroups(g GenerationDirectory, mode string) []generationProfileGroup {
	groups := []generationProfileGroup{}
	seen := map[string]bool{}
	for _, group := range generationProfileGroups(g) {
		if (group.Key == "agent") != (mode == "agent_bridge") {
			continue
		}
		for _, item := range group.Items {
			seen[item.Row.Profile.BridgeKind] = true
		}
		groups = append(groups, group)
	}
	if mode == "agent_bridge" {
		if len(groups) == 0 {
			groups = append(groups, generationProfileGroup{Key: "agent", Title: "Agent CLI"})
		}
		for _, kind := range []string{"codex", "traex", "pi", "antigravity"} {
			profile, ok := g.AgentBridges[kind]
			if !ok || seen[kind] {
				continue
			}
			groups[0].Items = append(groups[0].Items, generationProfileItem{ID: "agent-" + kind, Row: GenerationRow{Profile: profile}, Title: generationBridgeLabel(kind), Icon: kind, Detail: generationDefaultModel(g, kind)})
		}
		order := map[string]int{"codex": 0, "pi": 1, "traex": 2, "antigravity": 3}
		sort.SliceStable(groups[0].Items, func(i, j int) bool { return order[groups[0].Items[i].Icon] < order[groups[0].Items[j].Icon] })
	}
	return groups
}
