package search

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/zwhu/memlane/app/api"
)

func (c *ControllerOf[T, L]) APIFetcher(scope string) Fetcher[T, L] {
	searcher := c.Searcher
	if searcher == nil && c.Client != nil {
		searcher = c.Client
	}
	projects := c.catalog(scope)
	allowed := make(map[string]bool, len(projects))
	for _, p := range projects {
		if p.DatasetID != "" {
			allowed[p.DatasetID] = true
		}
	}
	request := api.SearchRequest{Filter: "all", Limit: 30, Semantic: true}
	if scope != "" && len(projects) == 1 {
		request.DatasetID = projects[0].DatasetID
		if !projects[0].Common && scope != "@common" {
			request.ProjectID = projects[0].ID
		}
	}
	convert, makeClient := c.FromResults, c.ClientFactory
	return func(ctx context.Context, query string, lanes L, publish func(Publication[T, L])) {
		p := Publication[T, L]{Sequence: 1, Lanes: lanes, Loaded: true, Done: true}
		defer func() { publish(p) }()
		if len(allowed) == 0 {
			return
		}
		var rows []ResultItem
		client := searcher
		if client == nil && makeClient != nil {
			client, p.Err = makeClient()
		}
		if p.Err == nil {
			rows, p.Err = readAPIResults(ctx, client, request, query, allowed)
		}
		p.Loaded = p.Err == nil
		if p.Err != nil {
			p.Message = "搜索失败：" + p.Err.Error()
			if query == "" {
				p.Message = "最近内容读取失败：" + p.Err.Error()
			}
		}
		if convert != nil {
			p.Rows = convert(rows)
		}
	}
}

func readAPIResults(ctx context.Context, client Searcher, request api.SearchRequest, query string, allowed map[string]bool) ([]ResultItem, error) {
	if client == nil {
		return nil, fmt.Errorf("控制面服务不可用")
	}
	if query == "" {
		resp, err := client.RecentSearch(ctx, 20)
		if err != nil {
			return nil, err
		}
		return MapRecentResponse(resp, allowed), nil
	}
	request.Query = query
	resp, err := client.Search(ctx, request)
	if err != nil {
		return nil, err
	}
	return MapSearchResponse(resp, query, allowed), nil
}

func MapSearchResponse(resp api.SearchResponse, query string, allowedDatasets map[string]bool) []ResultItem {
	var out []ResultItem
	byDocument := make(map[[2]string]int)
	for _, row := range resp.Items {
		i := mapCatalogItem(row.CatalogItem)
		if !allowedDatasets[i.DatasetID] || i.ID == "" {
			continue
		}
		i.UserID = row.UserID
		if i.IsSession {
			i.Date = row.LastActivityAt
		}
		i.SearchMatch = "关键词匹配"
		if i.Kind == "会话" {
			i.IsSession = true
		}
		if i.SourceDocument != "" {
			byDocument[[2]string{i.DatasetID, i.SourceDocument}] = len(out)
		}
		out = append(out, i)
	}
	for _, hit := range resp.SemanticHits {
		if !allowedDatasets[hit.DatasetID] || hit.DocumentID == "" {
			continue
		}
		if index, found := byDocument[[2]string{hit.DatasetID, hit.DocumentID}]; found {
			if out[index].Preview == "" {
				out[index].Preview = hit.Snippet
			}
			continue
		}
		i := ResultItem{ID: hit.DocumentID, DatasetID: hit.DatasetID, Kind: "知识片段", SourceDocument: hit.DocumentID,
			Title: readableCatalogTitle(hit.Title), Preview: hit.Snippet, SearchMatch: "语义相关"}
		if KeywordMatch(i.Title, i.Preview, query) {
			i.SearchMatch = "关键词匹配"
		}
		byDocument[[2]string{i.DatasetID, i.ID}] = len(out)
		out = append(out, i)
	}
	return out
}

func MapRecentResponse(resp api.RecentSearchResponse, allowedDatasets map[string]bool) []ResultItem {
	var out []ResultItem
	for _, row := range resp.RecentItems {
		i := mapCatalogItem(row)
		if i.ID != "" && allowedDatasets[i.DatasetID] {
			out = append(out, i)
		}
	}
	return out
}

var searchMachineName = regexp.MustCompile(`^(?:text_[0-9a-fA-F]{32}(?:\.(?:md|txt))?|(?:user_context|memory)-[0-9a-fA-F]{32}(?:\.txt)?|[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})$`)

func readableCatalogTitle(s string) string {
	s = strings.TrimSpace(s)
	if searchMachineName.MatchString(s) {
		return ""
	}
	return s
}

func mapCatalogItem(row api.CatalogItem) ResultItem {
	rawKind := strings.TrimSpace(row.Kind)
	kind := rawKind
	switch strings.ToLower(rawKind) {
	case "knowledge", "lesson", "common", "direct_memory", "user_context", "memory", "知识":
		kind = "知识"
	case "session", "raw_session", "会话":
		kind = "会话"
	case "raw", "raw_doc", "raw_document", "document", "文档":
		kind = "文档"
	default:
		if strings.TrimSpace(row.CommonCategory) != "" {
			kind = "知识"
		}
	}
	id := row.DocID
	if id == "" {
		id = row.DocumentID
	}
	if id == "" && kind == "会话" {
		id = row.SessionID
	}
	title := readableCatalogTitle(row.Title)
	if title == "" {
		title = readableCatalogTitle(row.DocLabel)
	}
	if title == "" {
		title = readableCatalogTitle(row.DocName)
	}
	return ResultItem{ID: id, ProjectID: row.ProjectID, DatasetID: row.DatasetID, Kind: kind,
		SourceDocument: row.DocumentID, Title: title, Date: row.EffectiveDate, Category: row.Category, Unread: row.IsUnread, IsSession: kind == "会话"}
}
