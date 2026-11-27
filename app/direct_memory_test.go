package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDirectOfficialMemoryReadAndSearch(t *testing.T) {
	body := "只复用官方能力；找不到公开扩展点时先说明缺口。\n"
	direct := Document{ID: "direct", Dataset: "project", Name: "user_context-0123456789abcdef0123456789abcdef.txt", Created: "2026-11-17T01:00:00Z", Metadata: map[string]any{"node_set": []string{"user_context"}}}
	material := Document{ID: "material", Dataset: "project", Name: "notes.md", Metadata: map[string]any{"node_set": []string{"project_docs"}}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			t.Error("wrong credential")
		}
		switch r.URL.Path {
		case "/api/knowledge":
			json.NewEncoder(w).Encode(map[string]any{
				"project_id": "p", "dataset_id": "project",
				"items": []map[string]any{
					{"doc_id": direct.ID, "dataset_id": direct.Dataset, "kind": "direct_memory", "doc_name": direct.Name, "created_at": direct.Created},
				},
			})
		case "/api/raw-records":
			json.NewEncoder(w).Encode(map[string]any{
				"items": []map[string]any{
					{"kind": "document", "doc_id": material.ID, "dataset_id": material.Dataset, "doc_name": material.Name},
				},
			})
		case "/api/knowledge/project/direct/body":
			json.NewEncoder(w).Encode(map[string]any{"dataset_id": "project", "document_id": "direct", "content": body, "mime_type": "text/plain"})
		case "/api/search":
			json.NewEncoder(w).Encode(map[string]any{
				"ok": true, "items": []any{},
				"semantic_hits": []map[string]any{
					{"dataset_id": "project", "document_id": "direct", "title": direct.Name, "snippet": body, "metadata": direct.Metadata},
				},
			})
		default:
			t.Error("unexpected route", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	c := Client{Config: Config{ProjectControl: readingControlConfig(t, server.URL)}}
	items, _, err := c.documents(context.Background(), Project{ID: "p", Dataset: "project"}, 0, false)
	if err != nil || len(items) != 1 || items[0].ID != direct.ID || items[0].Source != "" {
		t.Fatal(items, err)
	}
	i, err := c.read(context.Background(), items[0])
	if err != nil || i.Body != body || !i.BodyLoaded {
		t.Fatal(i, err)
	}
	p := itemPresentation(i)
	if p.VisibleBody != body || p.Unrecognized || p.WrapperRecognized || p.DisplayTitle == direct.Name {
		t.Fatal("direct save changed or treated as distilled lesson", p)
	}
	if fields := knowledgeBasisFields(i); len(fields) != 1 || fields[0].Value != "直接保存" {
		t.Fatal("invented model classification", fields)
	}
	raw, _, err := c.documents(context.Background(), Project{ID: "p", Dataset: "project"}, 0, true)
	if err != nil || len(raw) != 1 || raw[0].ID != material.ID {
		t.Fatal("project documents promoted or memory duplicated", raw, err)
	}
	results, err := c.search(context.Background(), []Project{{Dataset: "project"}}, "官方能力", "知识")
	if err != nil || len(results) != 1 || results[0].Kind != "知识片段" || results[0].SourceDocument != direct.ID {
		t.Fatal(results, err)
	}
	item := (v2KnowledgeItem{DocID: direct.ID, DatasetID: direct.Dataset, Kind: "direct_memory"}).toItem("")
	if item.ID != direct.ID || !documentKnowledge(*item.Document) || item.Source != "" || documentKnowledge(material) {
		t.Fatal("direct memory or raw material classified incorrectly", item)
	}
}

func TestDirectMemoryDoesNotPromoteSourceMaterial(t *testing.T) {
	// The official Data API stores upload name and extension separately.
	for _, name := range []string{"user_context-0123456789abcdef0123456789abcdef", "user_context-0123456789abcdef0123456789abcdef.txt"} {
		if readableDocumentTitle(Document{Name: name}) != "" {
			t.Fatal("upload hash presented as memory title", name)
		}
	}
	for _, set := range []string{"project_docs", "agent_actions", "user_sessions_from_cache", "agent_trace_feedbacks", "session_learnings:orphan"} {
		d := Document{Metadata: map[string]any{"node_set": []any{set}}}
		if documentKnowledge(d) || documentDirectMemory(d) {
			t.Fatal("source material promoted", set)
		}
	}
	for _, set := range []string{"user_context", "memory"} {
		d := Document{Metadata: map[string]any{"node_set": []any{set}}}
		if !documentKnowledge(d) || !documentDirectMemory(d) || documentSession(d) != "" {
			t.Fatal("missing direct save or invented session", set)
		}
	}
	d := Document{Metadata: map[string]any{"node_set": []string{"memory", "session_learnings"}}}
	if !documentKnowledge(d) || documentDirectMemory(d) {
		t.Fatal("distilled lesson provenance was replaced")
	}
}
