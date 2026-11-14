package simpledesktop

import (
	"context"
	"strings"
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

func (c Client) searchBoth(ctx context.Context, projects []Project, query string) ([]Item, error) {
	// Reuse both official retrieval lanes. Lexical top-k also includes zero
	// scores, so only visible literal hits may be labelled keyword matches.
	lexical, err := c.searchType(ctx, projects, query, "CHUNKS_LEXICAL")
	if err != nil {
		return nil, err
	}
	semantic, err := c.search(ctx, projects, query)
	if err != nil {
		return nil, err
	}
	rows := []Item{}
	seen := map[string]bool{}
	for _, i := range lexical {
		if keywordMatch(i, query) && !seen[i.key()] {
			i.SearchMatch = "关键词匹配"
			rows = append(rows, i)
			seen[i.key()] = true
		}
	}
	for _, i := range semantic {
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
	return rows, nil
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
