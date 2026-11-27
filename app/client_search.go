package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/zwhu/memlane/app/api"
)

func (c Client) search(ctx context.Context, projects []Project, q string, filters ...string) ([]Item, error) {
	return c.searchType(ctx, projects, q, "CHUNKS", filters...)
}

func (c Client) searchType(ctx context.Context, projects []Project, q, searchType string, filters ...string) ([]Item, error) {
	ids := []string{}
	for _, p := range projects {
		if p.Dataset != "" {
			ids = append(ids, p.Dataset)
		}
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("请先配置项目与官方数据组关联")
	}
	control, ok := c.controlClient()
	if !ok {
		return nil, fmt.Errorf("本地网关尚未配置")
	}
	_, baseIdentity, _ := readingIdentityPair(c)
	apiClient, err := newAppAPIClient(control, baseIdentity)
	if err != nil {
		return nil, err
	}
	filter := ""
	if len(filters) > 0 {
		filter = filters[0]
	}
	apiFilter := "all"
	if filter == "原始材料" {
		apiFilter = "raw"
	}
	req := api.SearchRequest{
		Query:    q,
		Filter:   apiFilter,
		Limit:    30,
		Semantic: searchType != "CHUNKS_LEXICAL",
	}
	if len(projects) == 1 {
		req.DatasetID = projects[0].Dataset
	}
	resp, err := apiClient.Search(ctx, req)
	if err != nil {
		return nil, err
	}
	validDataset := map[string]bool{}
	for _, id := range ids {
		validDataset[id] = true
	}
	items := []Item{}
	seen := map[string]bool{}
	for _, row := range resp.Items {
		if !validDataset[row.DatasetID] {
			continue
		}
		lowerKind := strings.ToLower(strings.TrimSpace(row.Kind))
		if lowerKind == "session" || lowerKind == "raw_session" || lowerKind == "会话" || (row.SessionID != "" && row.DocID == "" && row.DocumentID == "") {
			if filter == "知识" {
				continue
			}
			sess := Session{
				ID: row.SessionID, Dataset: row.DatasetID, User: row.UserID, Agent: row.AgentID,
				Started: row.StartedAt, Updated: row.LastActivityAt,
				IsSubagent: row.ParentSessionID != "", ParentSessionID: row.ParentSessionID,
				SubagentRole: row.SubagentRole, NestingDepth: row.NestingDepth,
			}
			title := row.Title
			if title == "" {
				title = row.SessionID
			}
			item := Item{ID: row.SessionID, Dataset: row.DatasetID, Kind: "会话", Title: title, Date: row.LastActivityAt, Session: &sess}
			if !seen[item.Key()] {
				seen[item.Key()] = true
				items = append(items, item)
			}
			continue
		}
		catRow := catalogRowFromAPI(row.CatalogItem)
		if catRow.DocID == "" {
			continue
		}
		item := catRow.toItem("")
		isKnowledge := item.Kind == "知识"
		if filter == "知识" && !isKnowledge {
			continue
		}
		if filter == "原始材料" && isKnowledge {
			continue
		}
		if isKnowledge && catRow.Title != "" && readableName(catRow.Title) == "" {
			catRow.Title = ""
			item = catRow.toItem("")
		}
		if !seen[item.Key()] {
			seen[item.Key()] = true
			items = append(items, item)
		}
	}
	for _, hit := range resp.SemanticHits {
		if !validDataset[hit.DatasetID] {
			return nil, fmt.Errorf("official search dataset changed")
		}
		if hit.DocumentID == "" || hit.Snippet == "" {
			continue
		}
		title := hit.Title
		if title == "" {
			title = "检索片段"
		}
		contentType := "原始材料片段"
		if documentKnowledge(Document{Metadata: hit.Metadata}) {
			contentType = "知识片段"
		}
		if filter == "知识" && contentType != "知识片段" {
			continue
		}
		if filter == "原始材料" && contentType == "知识片段" {
			continue
		}
		item := Item{
			ID:             hit.DocumentID,
			Dataset:        hit.DatasetID,
			Kind:           contentType,
			Title:          title,
			Body:           hit.Snippet,
			Preview:        hit.Snippet,
			SourceDocument: hit.DocumentID,
			BodyLoaded:     true,
		}
		item.CommonCategory = commonCategory(Document{Metadata: hit.Metadata})
		item.Title = itemPresentation(item).DisplayTitle
		if !seen[item.Key()] {
			seen[item.Key()] = true
			items = append(items, item)
		}
	}
	return items, nil
}

// Resolve a real official document id against the current dataset catalog.
// A title, filename or graph path is never used as a substitute for identity.
