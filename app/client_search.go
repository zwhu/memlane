package app

import (
	"context"
	"encoding/json"
	"fmt"
)

func (c Client) search(ctx context.Context, projects []Project, q string, filters ...string) ([]Item, error) {
	return c.searchType(ctx, projects, q, "CHUNKS", filters...)
}

func (c Client) searchType(ctx context.Context, projects []Project, q, searchType string, filters ...string) ([]Item, error) {
	ids := []string{}
	for _, p := range projects {
		ids = append(ids, p.Dataset)
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("请先配置项目与官方数据组关联")
	}
	input := map[string]any{"query": q, "search_type": searchType, "dataset_ids": ids, "only_context": false, "top_k": 30}
	filter := ""
	if len(filters) > 0 {
		filter = filters[0]
	}
	if filter == "知识" {
		input["node_name"] = []string{"session_learnings", "memlane_common", "user_context", "memory"}
		input["node_name_filter_operator"] = "OR"
	}
	b, e := c.request(ctx, "POST", "/api/v1/search", input)
	if e != nil {
		return nil, e
	}
	var groups []struct {
		Dataset string           `json:"dataset_id"`
		Results []map[string]any `json:"search_result"`
	}
	if e = json.Unmarshal(b, &groups); e != nil {
		return nil, e
	}
	items := []Item{}
	validDataset := map[string]bool{}
	for _, id := range ids {
		validDataset[id] = true
	}
	for _, g := range groups {
		if !validDataset[g.Dataset] {
			return nil, fmt.Errorf("official search dataset changed")
		}
		for _, r := range g.Results {
			stringField := func(k string) string { s, _ := r[k].(string); return s }
			id := stringField("id")
			text := stringField("text")
			if id == "" || text == "" {
				continue
			}
			title := stringField("document_name")
			if title == "" {
				title = "检索片段"
			}
			contentType := "原始材料片段"
			var metadata map[string]any
			if raw := stringField("external_metadata"); raw != "" {
				_ = json.Unmarshal([]byte(raw), &metadata)
			} else if m, ok := r["external_metadata"].(map[string]any); ok {
				metadata = m
			}
			if documentKnowledge((Document{Metadata: metadata})) {
				contentType = "知识片段"
			}
			if filter == "原始材料" && contentType == "知识片段" {
				continue
			}
			item := Item{ID: id, Dataset: g.Dataset, Kind: contentType, Title: title, Body: text, Preview: text, SourceDocument: stringField("document_id"), BodyLoaded: true}
			item.CommonCategory = commonCategory(Document{Metadata: metadata})
			item.Title = itemPresentation(item).DisplayTitle
			items = append(items, item)
		}
	}
	return items, nil
}

// Resolve a real official document id against the current dataset catalog.
// A title, filename or graph path is never used as a substitute for identity.
