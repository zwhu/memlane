package simpledesktop

import (
	"context"
	"errors"
	appsearch "github.com/zwhu/memlane/app/search"
	"sync"
	"time"
)

var lexicalSearchSoftTimeout = 3 * time.Second

func keywordMatch(i Item, query string) bool {
	title, body := searchRowText(i)
	return appsearch.KeywordMatch(title, body, query)
}

type searchLaneResult struct {
	Metadata, Keyword, Semantic                []Item
	MetadataOK, KeywordOK, SemanticOK          bool
	MetadataError, KeywordError, SemanticError error
	KeywordTimedOut                            bool
}

func (r searchLaneResult) err() error {
	return errors.Join(r.MetadataError, r.KeywordError, r.SemanticError)
}

func (r searchLaneResult) hasAnySuccess() bool {
	return r.MetadataOK || r.KeywordOK || r.SemanticOK
}

func (r searchLaneResult) message() string {
	if r.KeywordTimedOut && (r.MetadataOK || r.SemanticOK) {
		if r.SemanticError != nil && !r.MetadataOK {
			return "词法全文检索超时，请稍后重试"
		}
		return "词法全文检索超时，已展示元数据与语义结果"
	}
	if r.MetadataOK && r.KeywordError != nil && r.SemanticError != nil {
		return "语义与词法检索暂不可用，已展示元数据匹配"
	}
	if r.KeywordError != nil && r.SemanticError != nil {
		return "搜索失败"
	}
	if r.SemanticError != nil {
		return "暂时只能按关键词查找"
	}
	if r.KeywordError != nil {
		return "暂时无法按关键词匹配"
	}
	if r.MetadataError != nil && !r.KeywordOK && !r.SemanticOK {
		return controlUserError(r.MetadataError)
	}
	return ""
}

// A retry keeps even a successful empty lane. The caller binds this snapshot
// to its query, dataset catalog and reading identity before it may be reused.
func (c Client) searchLanes(ctx context.Context, projects []Project, query string, previous searchLaneResult) searchLaneResult {
	r := previous
	var wg sync.WaitGroup
	if !r.KeywordOK {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.Keyword, r.KeywordError = c.searchType(ctx, projects, query, "CHUNKS_LEXICAL")
			r.KeywordOK = r.KeywordError == nil
		}()
	}
	if !r.SemanticOK {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.Semantic, r.SemanticError = c.search(ctx, projects, query)
			r.SemanticOK = r.SemanticError == nil
		}()
	}
	wg.Wait()
	return r
}

func (c Client) searchBoth(ctx context.Context, projects []Project, query string) ([]Item, error) {
	r := c.searchLanes(ctx, projects, query, searchLaneResult{})
	return r.rows(query), r.err()
}

func (r searchLaneResult) rows(query string) []Item {
	// Reuse all retrieval lanes: instant v2 metadata hits, official lexical
	// hits (filtered for visible literal matches), and semantic hits.
	rows := []Item{}
	seen := map[string]bool{}
	for _, i := range r.Metadata {
		if seen[i.key()] {
			continue
		}
		i.SearchMatch = "关键词匹配"
		rows = append(rows, i)
		seen[i.key()] = true
	}
	for _, i := range r.Keyword {
		if keywordMatch(i, query) && !seen[i.key()] {
			i.SearchMatch = "关键词匹配"
			rows = append(rows, i)
			seen[i.key()] = true
		}
	}
	for _, i := range r.Semantic {
		if seen[i.key()] {
			continue
		}
		i.SearchMatch = "语义相关"
		if keywordMatch(i, query) {
			i.SearchMatch = "关键词匹配"
		}
		rows = append(rows, i)
		seen[i.key()] = true
	}
	return rows
}

func searchHitText(i Item, query string) (string, string) {
	title, preview := searchRowText(i)
	if i.SearchMatch != "关键词匹配" || preview == "" {
		return title, preview
	}
	return title, appsearch.HitExcerpt(preview, query)
}
