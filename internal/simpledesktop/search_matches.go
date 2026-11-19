package simpledesktop

import (
	"context"
	"errors"
	"strings"
	"sync"
)

func keywordMatch(i Item, query string) bool {
	title, body := searchRowText(i)
	text := strings.ToLower(title + "\n" + body)
	terms := strings.Fields(strings.ToLower(query))
	if len(terms) == 0 {
		return false
	}
	for _, term := range terms {
		if !strings.Contains(text, term) {
			return false
		}
	}
	return true
}

type searchLaneResult struct {
	Keyword, Semantic           []Item
	KeywordOK, SemanticOK       bool
	KeywordError, SemanticError error
}

func (r searchLaneResult) err() error { return errors.Join(r.KeywordError, r.SemanticError) }

func (r searchLaneResult) message() string {
	if r.KeywordError != nil && r.SemanticError != nil {
		return "搜索失败"
	}
	if r.SemanticError != nil {
		return "暂时只能按关键词查找"
	}
	if r.KeywordError != nil {
		return "暂时无法按关键词匹配"
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
	// Reuse both official retrieval lanes. Lexical top-k also includes zero
	// scores, so only visible literal hits may be labelled keyword matches.
	rows := []Item{}
	seen := map[string]bool{}
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
	// Excerpt around the actual hit instead of always showing the chunk header.
	runes := []rune(preview)
	lower := []rune(strings.ToLower(preview))
	for _, term := range strings.Fields(strings.ToLower(query)) {
		needle := []rune(term)
		for start := 0; start+len(needle) <= len(lower); start++ {
			if string(lower[start:start+len(needle)]) != term {
				continue
			}
			from, to := max(0, start-50), min(len(runes), start+len(needle)+130)
			text := string(runes[from:to])
			if from > 0 {
				text = "…" + text
			}
			if to < len(runes) {
				text += "…"
			}
			return title, text
		}
	}
	return title, preview
}
