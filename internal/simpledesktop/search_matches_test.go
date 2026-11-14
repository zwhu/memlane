package simpledesktop

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
		if in["query"] != "库存" || in["only_context"] != false {
			t.Error("unexpected request", in)
		}
		rows := []map[string]any{{"id": "semantic", "text": "并发更新需要事务保护", "document_id": "doc-semantic"}}
		if in["search_type"] == "CHUNKS_LEXICAL" {
			rows = []map[string]any{
				{"id": "literal", "text": "Session ID: synthetic\n\n库存扣减使用原子更新", "document_id": "doc-literal"},
				{"id": "zero", "text": "unrelated zero-score top-k candidate"},
			}
		} else if in["search_type"] != "CHUNKS" {
			t.Error("unexpected retrieval lane")
		}
		json.NewEncoder(w).Encode([]map[string]any{{"dataset_id": "p", "search_result": rows}})
	}))
	defer server.Close()
	client := Client{Config: Config{ServiceURL: server.URL}}
	rows, err := client.searchBoth(context.Background(), []Project{{Dataset: "p"}}, "库存")
	if err != nil || len(rows) != 2 {
		t.Fatalf("%+v %v", rows, err)
	}
	if rows[0].SearchMatch != "关键词匹配" || rows[0].ID != "literal" || rows[0].SourceDocument != "doc-literal" || rows[1].SearchMatch != "语义相关" {
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
