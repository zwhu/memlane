package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientSearchKindConsistencyAndMachineNameFiltering(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"items": []map[string]any{
				{"dataset_id": "ds", "doc_id": "k-direct", "kind": "direct_memory", "title": "直接保存记忆"},
				{"dataset_id": "ds", "doc_id": "k-uctx", "kind": "USER_CONTEXT", "title": "用户上下文记忆"},
				{"dataset_id": "ds", "doc_id": "k-mem", "kind": "memory", "title": "简单记忆"},
				{"dataset_id": "ds", "doc_id": "k-common", "kind": "common", "common_category": "规范", "title": "通用记忆"},
				{"dataset_id": "ds", "doc_id": "k-machine", "kind": "  Knowledge ", "title": "text_0123456789abcdef0123456789abcdef", "doc_name": "text_0123456789abcdef0123456789abcdef", "effective_date": "2026-11-21"},
				{"dataset_id": "ds", "doc_id": "d-raw", "kind": "raw_doc", "title": "README.md", "doc_name": "README.md"},
				{"dataset_id": "ds", "doc_id": "d-rawdoc", "kind": "RAW_DOCUMENT", "title": "spec.md", "doc_name": "spec.md"},
				{"dataset_id": "ds", "session_id": "s-raw", "kind": "RAW_SESSION", "title": "Raw session"},
			},
			"semantic_hits": []any{},
		})
	}))
	defer server.Close()

	client := Client{Config: Config{ProjectControl: readingControlConfig(t, server.URL)}}
	projects := []Project{{ID: "p", Dataset: "ds"}}

	all, err := client.search(context.Background(), projects, "test")
	if err != nil {
		t.Fatal(err)
	}
	byID := make(map[string]Item, len(all))
	for _, item := range all {
		byID[item.ID] = item
	}
	for _, id := range []string{"k-direct", "k-uctx", "k-mem", "k-common", "k-machine"} {
		item, ok := byID[id]
		if !ok || item.Kind != "知识" {
			t.Fatalf("item %s expected Kind=知识, got %+v", id, item)
		}
	}
	for _, id := range []string{"k-direct", "k-uctx", "k-mem"} {
		item := byID[id]
		if item.Document == nil || !documentDirectMemory(*item.Document) {
			t.Fatalf("item %s lost direct memory metadata: %+v", id, item)
		}
	}
	if byID["k-machine"].Title != "记忆 · 2026-11-21" {
		t.Fatalf("machine title leaked in client search: %+v", byID["k-machine"])
	}
	for _, id := range []string{"d-raw", "d-rawdoc"} {
		item, ok := byID[id]
		if !ok || item.Kind != "文档" {
			t.Fatalf("item %s expected Kind=文档, got %+v", id, item)
		}
	}
	if byID["s-raw"].Kind != "会话" || byID["s-raw"].Session == nil {
		t.Fatalf("raw_session was not mapped to 会话: %+v", byID["s-raw"])
	}

	knowledgeOnly, err := client.search(context.Background(), projects, "test", "知识")
	if err != nil {
		t.Fatal(err)
	}
	if len(knowledgeOnly) != 5 {
		t.Fatalf("knowledge filter dropped or leaked items: got %d (%+v)", len(knowledgeOnly), knowledgeOnly)
	}
	for _, item := range knowledgeOnly {
		if item.Kind != "知识" {
			t.Fatalf("knowledge filter returned non-knowledge item: %+v", item)
		}
	}

	rawOnly, err := client.search(context.Background(), projects, "test", "原始材料")
	if err != nil {
		t.Fatal(err)
	}
	if len(rawOnly) != 3 {
		t.Fatalf("raw filter dropped or leaked items: got %d (%+v)", len(rawOnly), rawOnly)
	}
	for _, item := range rawOnly {
		if item.Kind == "知识" {
			t.Fatalf("raw filter returned knowledge item: %+v", item)
		}
	}
}
