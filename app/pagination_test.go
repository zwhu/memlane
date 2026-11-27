package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// These are HTTP fixtures verifying gateway-backed cursor pagination for knowledge, raw documents, and sessions.
func TestOfficialDocumentPaginationLookaheadAndLastPage(t *testing.T) {
	for _, raw := range []bool{false, true} {
		for _, total := range []int{0, 30, 31, 61} {
			t.Run(fmt.Sprintf("raw=%t/total=%d", raw, total), func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					start := 0
					if cur := r.URL.Query().Get("cursor"); cur != "" {
						_, _ = fmt.Sscanf(cur, "page-%d", &start)
					}
					end := min(start+30, total)
					hasMore := end < total
					nextCursor := ""
					if hasMore {
						nextCursor = fmt.Sprintf("page-%d", end)
					}
					if raw {
						if r.URL.Path != "/api/raw-records" {
							t.Errorf("unexpected endpoint %s", r.URL.Path)
							w.WriteHeader(http.StatusNotFound)
							return
						}
						items := make([]map[string]any, 0, max(0, end-start))
						for i := start; i < end; i++ {
							items = append(items, map[string]any{
								"kind":       "document",
								"doc_id":     fmt.Sprintf("fixture-document-%03d", i),
								"dataset_id": "fixture-dataset",
								"doc_name":   "fixture.md",
							})
						}
						_ = json.NewEncoder(w).Encode(map[string]any{"items": items, "has_more": hasMore, "next_cursor": nextCursor})
						return
					}
					if r.URL.Path != "/api/knowledge" {
						t.Errorf("unexpected endpoint %s", r.URL.Path)
						w.WriteHeader(http.StatusNotFound)
						return
					}
					items := make([]map[string]any, 0, max(0, end-start))
					for i := start; i < end; i++ {
						items = append(items, map[string]any{
							"doc_id":     fmt.Sprintf("fixture-document-%03d", i),
							"dataset_id": "fixture-dataset",
							"kind":       "knowledge",
							"doc_name":   "fixture.md",
						})
					}
					_ = json.NewEncoder(w).Encode(map[string]any{
						"project_id": "p", "dataset_id": "fixture-dataset",
						"items": items, "has_more": hasMore, "next_cursor": nextCursor,
					})
				}))
				defer server.Close()
				client := Client{Config: Config{ProjectControl: readingControlConfig(t, server.URL)}}
				seen := make([]string, 0, total)
				for offset := 0; ; offset += 30 {
					items, more, err := client.documents(context.Background(), Project{ID: "p", Dataset: "fixture-dataset"}, offset, raw)
					if err != nil {
						t.Fatal(err)
					}
					wantCount := min(30, total-offset)
					if len(items) != wantCount {
						t.Fatalf("offset %d: got %d visible rows, want %d", offset, len(items), wantCount)
					}
					if wantMore := total-offset > 30; more != wantMore {
						t.Fatalf("offset %d: more=%t, want %t", offset, more, wantMore)
					}
					for index, item := range items {
						wantID := fmt.Sprintf("fixture-document-%03d", offset+index)
						if item.ID != wantID {
							t.Fatalf("offset %d row %d: got %s, want %s", offset, index, item.ID, wantID)
						}
						seen = append(seen, item.ID)
					}
					if !more {
						break
					}
				}
				if len(seen) != total {
					t.Fatalf("collected %d documents, want %d", len(seen), total)
				}
			})
		}
	}
}

func TestOfficialDocumentPaginationKeepsServerOffsetAcrossTypeFiltering(t *testing.T) {
	page := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/knowledge" {
			t.Errorf("unexpected request %s", r.URL.String())
			w.WriteHeader(http.StatusNotFound)
			return
		}
		page++
		items := []map[string]any{}
		if page == 1 {
			items = append(items, map[string]any{"doc_id": "fixture-document-030", "dataset_id": "fixture-dataset", "kind": "knowledge", "doc_name": "fixture.md"})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"project_id":  "p",
			"dataset_id":  "fixture-dataset",
			"has_more":    true,
			"next_cursor": fmt.Sprintf("page-%d", page),
			"items":       items,
		})
	}))
	defer server.Close()
	client := Client{Config: Config{ProjectControl: readingControlConfig(t, server.URL)}}
	project := Project{ID: "p", Dataset: "fixture-dataset"}
	items, more, err := client.documents(context.Background(), project, 0, false)
	if err != nil || len(items) != 1 || items[0].ID != "fixture-document-030" || !more {
		t.Fatalf("gateway knowledge page mismatch: rows=%+v more=%t err=%v", items, more, err)
	}
}

func TestOfficialDocumentPaginationRejectsEmptyOrDuplicateCursor(t *testing.T) {
	for _, tc := range []struct {
		name       string
		nextCursor string
	}{
		{"empty_cursor_with_has_more", ""},
		{"duplicate_cursor_with_has_more", "stuck-cursor"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"project_id":  "p",
					"dataset_id":  "fixture-dataset",
					"has_more":    true,
					"next_cursor": tc.nextCursor,
					"items": []map[string]any{
						{"doc_id": "d1", "dataset_id": "fixture-dataset", "kind": "knowledge", "doc_name": "fixture.md"},
					},
				})
			}))
			defer server.Close()
			client := Client{Config: Config{ProjectControl: readingControlConfig(t, server.URL)}}
			offset := 0
			if tc.nextCursor != "" {
				offset = 30
			}
			_, more, err := client.documents(context.Background(), Project{ID: "p", Dataset: "fixture-dataset"}, offset, false)
			if err == nil || more {
				t.Fatalf("expected error and more=false on %s, got more=%v err=%v", tc.name, more, err)
			}
		})
	}
}
