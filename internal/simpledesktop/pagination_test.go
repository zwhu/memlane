package simpledesktop

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"testing"
)

// These are HTTP fixtures, not a claim that the daily store contains enough
// documents to have exercised production pagination in the Native window.
func TestOfficialDocumentPaginationLookaheadAndLastPage(t *testing.T) {
	for _, raw := range []bool{false, true} {
		for _, total := range []int{0, 30, 31, 60, 61} {
			t.Run(fmt.Sprintf("raw=%t/total=%d", raw, total), func(t *testing.T) {
				rows := paginationDocuments(total, raw)
				requested := make(chan int, 8)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path != "/api/v1/datasets/fixture-dataset/data" {
						t.Errorf("unexpected endpoint %s", r.URL.Path)
						w.WriteHeader(http.StatusNotFound)
						return
					}
					if r.URL.Query().Get("limit") != "31" {
						t.Errorf("the official list must request one lookahead row: %s", r.URL.RawQuery)
					}
					offset, err := strconv.Atoi(r.URL.Query().Get("offset"))
					if err != nil || offset < 0 || offset > total {
						t.Errorf("invalid official offset %q", r.URL.Query().Get("offset"))
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					requested <- offset
					w.Header().Set("Content-Type", "application/json")
					if err := json.NewEncoder(w).Encode(rows[offset:min(offset+31, total)]); err != nil {
						t.Error(err)
					}
				}))
				defer server.Close()
				client := Client{Config: Config{ServiceURL: server.URL}}
				seen := make([]string, 0, total)
				var expectedOffsets []int
				for offset := 0; ; offset += 30 {
					expectedOffsets = append(expectedOffsets, offset)
					items, more, err := client.documents(context.Background(), Project{Dataset: "fixture-dataset"}, offset, raw)
					if err != nil {
						t.Fatal(err)
					}
					wantCount := min(30, total-offset)
					if len(items) != wantCount {
						t.Fatalf("offset %d: got %d visible rows, want %d (lookahead must stay hidden)", offset, len(items), wantCount)
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
					if offset > total {
						t.Fatal("pagination did not terminate")
					}
				}
				var offsets []int
				for range expectedOffsets {
					offsets = append(offsets, <-requested)
				}
				if !reflect.DeepEqual(offsets, expectedOffsets) {
					t.Fatalf("requested offsets %v, want %v", offsets, expectedOffsets)
				}
				if len(seen) != total {
					t.Fatalf("collected %d unique ordered documents, want %d", len(seen), total)
				}
			})
		}
	}
}

// The official endpoint pages all documents before the client separates
// knowledge from raw documents. Offset must follow server rows, including a
// page that has no visible entries for the selected type.
func TestOfficialDocumentPaginationKeepsServerOffsetAcrossTypeFiltering(t *testing.T) {
	rows := paginationDocuments(31, true)
	rows[30] = paginationDocuments(1, false)[0]
	rows[30]["id"] = "fixture-document-030"
	requested := make(chan int, 8)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/datasets/fixture-dataset/data" || r.URL.Query().Get("limit") != "31" {
			t.Errorf("unexpected official request %s", r.URL.String())
		}
		offset, err := strconv.Atoi(r.URL.Query().Get("offset"))
		if err != nil || (offset != 0 && offset != 30) {
			t.Errorf("unexpected offset %q", r.URL.Query().Get("offset"))
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		requested <- offset
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(rows[offset:]); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	client := Client{Config: Config{ServiceURL: server.URL}}
	project := Project{Dataset: "fixture-dataset"}
	first, more, err := client.documents(context.Background(), project, 0, false)
	if err != nil || len(first) != 0 || !more {
		t.Fatalf("filtered first server page must keep next page available: rows=%d more=%t err=%v", len(first), more, err)
	}
	last, more, err := client.documents(context.Background(), project, 30, false)
	if err != nil || len(last) != 1 || last[0].ID != "fixture-document-030" || more {
		t.Fatalf("filtered next page lost lookahead identity: rows=%+v more=%t err=%v", last, more, err)
	}
	offsets := []int{<-requested, <-requested}
	if !reflect.DeepEqual(offsets, []int{0, 30}) {
		t.Fatalf("filtered pagination requested offsets %v", offsets)
	}
}

func paginationDocuments(total int, raw bool) []map[string]any {
	rows := make([]map[string]any, total)
	for index := range rows {
		row := map[string]any{"id": fmt.Sprintf("fixture-document-%03d", index), "datasetId": "fixture-dataset", "name": "fixture.md"}
		if !raw {
			row["externalMetadata"] = map[string]any{"node_set": []string{"session_learnings", "session_learnings:fixture-session"}}
		}
		rows[index] = row
	}
	return rows
}
