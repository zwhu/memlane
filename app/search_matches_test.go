package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSearchBothLabelsActualMatchesAndKeepsOfficialIdentity(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in map[string]any
		json.NewDecoder(r.Body).Decode(&in)
		if in["q"] != "库存" {
			t.Error("unexpected request", in)
		}
		if sem, _ := in["semantic"].(bool); !sem {
			json.NewEncoder(w).Encode(map[string]any{
				"ok": true,
				"items": []map[string]any{
					{"dataset_id": "p", "doc_id": "doc-literal", "kind": "knowledge", "title": "库存扣减使用原子更新"},
					{"dataset_id": "p", "doc_id": "zero", "kind": "knowledge", "title": "unrelated zero-score top-k candidate"},
				},
				"semantic_hits": []any{},
			})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"ok":    true,
			"items": []any{},
			"semantic_hits": []map[string]any{
				{"dataset_id": "p", "document_id": "doc-semantic", "title": "并发", "snippet": "并发更新需要事务保护"},
			},
		})
	}))
	defer server.Close()
	client := Client{Config: Config{ProjectControl: readingControlConfig(t, server.URL)}}
	rows, err := client.searchBoth(context.Background(), []Project{{Dataset: "p"}}, "库存")
	if err != nil || len(rows) != 2 {
		t.Fatalf("%+v %v", rows, err)
	}
	if rows[0].SearchMatch != "关键词匹配" || rows[0].ID != "doc-literal" || rows[1].SearchMatch != "语义相关" || rows[1].SourceDocument != "doc-semantic" {
		t.Fatal(rows)
	}
}

func TestSearchHitExcerptIncludesMatchingText(t *testing.T) {
	i := Item{Title: "A document", SearchMatch: "关键词匹配", Preview: strings.Repeat("before ", 100) + "库存扣减" + strings.Repeat(" after", 100)}
	title, excerpt := searchHitText(i, "库存")
	if title != "A document" || !strings.Contains(excerpt, "库存扣减") || len([]rune(excerpt)) > 200 {
		t.Fatal(title, excerpt)
	}
}
