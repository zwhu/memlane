package official

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/zwhu/memlane/internal/records"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestKnowledgeCatalogUsesOfficialFormatStatusAndCursor(t *testing.T) {
	for _, mode := range []string{"completed", "coding", "pending", "bad_header", "status_denied", "status_unavailable", "inconsistent_status", "changed_after_status"} {
		t.Run(mode, func(t *testing.T) {
			raw := documentTestRow("16a5ca85-2360-4d11-a4aa-78161f632096")
			row := documentTestRow("26a5ca85-2360-4d11-a4aa-78161f632096")
			row.Name = "text_0123456789abcdef0123456789abcdef"
			row.Metadata = json.RawMessage(`{"node_set":["session_learnings","session_learnings:scope-session"]}`)
			if mode == "coding" {
				row.Metadata = json.RawMessage(`{"node_set":["session_learnings","session_learnings:scope-session"],"lesson_type":"state","state_key":"atomic-update","session_id":"scope-session"}`)
			}
			statusRead := false
			rawReads := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.Header.Get("X-Api-Key") != "synthetic" {
					t.Error("unexpected write/principal")
					w.WriteHeader(403)
					return
				}
				switch {
				case strings.HasSuffix(r.URL.Path, "/identity"):
					json.NewEncoder(w).Encode(map[string]string{"instance": documentTestInstance, "cognee_version": "1.6.7"})
				case strings.HasSuffix(r.URL.Path, "/data"):
					rows := []records.OfficialDocument{raw, row}
					if statusRead && mode == "changed_after_status" {
						rows[1].Name = "changed"
					}
					at, _ := strconv.Atoi(r.URL.Query().Get("offset"))
					limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
					if at > len(rows) {
						at = len(rows)
					}
					json.NewEncoder(w).Encode(rows[at:min(at+limit, len(rows))])
				case strings.HasSuffix(r.URL.Path, "/raw"):
					rawReads++
					if strings.Contains(r.URL.Path, raw.ID) {
						t.Error("raw document fetched as knowledge")
					}
					if mode == "bad_header" {
						fmt.Fprint(w, "not a session learning")
					} else {
						if mode == "coding" {
							fmt.Fprint(w, "Lesson-Type: state\nState-Key: atomic-update\n")
						}
						fmt.Fprint(w, "# Session learning (session scope-session)\n\nUse an atomic update.\n\nKeep the affected-row check.")
					}
				case strings.HasSuffix(r.URL.Path, "/processing-status"):
					statusRead = true
					if mode == "status_denied" {
						w.WriteHeader(403)
						return
					}
					if mode == "status_unavailable" {
						w.WriteHeader(503)
						return
					}
					completed := mode != "pending"
					count := 1
					if !completed {
						count = 0
					}
					if mode == "inconsistent_status" {
						count = 2
					}
					json.NewEncoder(w).Encode(map[string]any{"total": 2, "completed": count, "pending": 2 - count, "items": []any{map[string]any{"id": raw.ID, "completed": false}, map[string]any{"id": row.ID, "completed": completed}}})
				default:
					t.Error("unexpected route")
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			reader := DocumentReader{SessionReader{Endpoint: server.URL, Instance: documentTestInstance, Dataset: documentTestDataset, Token: "synthetic"}}
			first, err := reader.ListKnowledgePage(context.Background(), 1, 0)
			if err != nil || len(first.Items) != 0 || !first.HasMore || first.Offset != 0 || rawReads != 0 {
				t.Fatal("raw-only page lost its upstream cursor", first, err)
			}
			page, err := reader.ListKnowledgePage(context.Background(), 1, 1)
			switch mode {
			case "status_denied", "status_unavailable", "inconsistent_status", "changed_after_status":
				if err == nil || len(page.Items) != 0 {
					t.Fatal("unverified knowledge escaped", page, err)
				}
			case "bad_header":
				if err != nil || len(page.Items) != 0 || statusRead {
					t.Fatal("marker alone promoted raw text", page, err)
				}
			default:
				if err != nil || len(page.Items) != 1 || page.HasMore || page.Offset != 1 {
					t.Fatal(page, err)
				}
				item := page.Items[0]
				selected, e := reader.ReadKnowledge(context.Background(), row, 1)
				if e != nil || selected.Content.Document.ID != item.Content.Document.ID || selected.ProcessingState != item.ProcessingState {
					t.Fatal("selected knowledge/status diverged", selected, e)
				}
				want := "completed"
				if mode == "pending" {
					want = "not_completed"
				}
				if item.ProcessingState != want || item.Title != "Use an atomic update." || !item.TitleFromBody || LearningBody(item.Content) != "Use an atomic update.\n\nKeep the affected-row check." {
					t.Fatal("official content/title/status changed", item)
				}
			}
		})
	}
}
func TestLearningTitlePreservesOfficialTitleAndBody(t *testing.T) {
	label := "Official user title"
	content := records.OfficialDocumentText{Document: records.OfficialDocument{Name: "official-note.md", Label: &label}, SessionReference: "s", Text: "# Session learning (session s)\n\nExact body."}
	if title, fallback := learningTitle(content); title != label || fallback {
		t.Fatal(title, fallback)
	}
	content.Document.Label = nil
	if title, fallback := learningTitle(content); title != "official-note.md" || fallback {
		t.Fatal(title, fallback)
	}
	content.Document.Name = "text_0123456789abcdef0123456789abcdef"
	content.Text = "# Session learning (session s)\n\n" + strings.Repeat("知", 70)
	title, fallback := learningTitle(content)
	if !fallback || len([]rune(title)) != 65 || !strings.HasSuffix(title, "…") {
		t.Fatal(title)
	}
	content.SessionReference = ""
	if LearningBody(content) != content.Text {
		t.Fatal("unverified heading stripped")
	}
}

func TestImportedKnowledgeKeepsItsOriginAndExactBody(t *testing.T) {
	text := "# Session learning (session literal-user-text)\n\nPreserved imported knowledge."
	digest := sha256.Sum256([]byte(text))
	base := map[string]any{"import_id": documentTestInstance, "library": documentTestInstance, "project": documentTestDataset, "memory": "e6a5ca85-2360-4d11-a4aa-78161f632096", "revision": 1, "origin": "knowledge", "digest": hex.EncodeToString(digest[:])}
	for _, mode := range []string{"knowledge", "raw", "wrong_body", "bad_identity", "zero_revision"} {
		t.Run(mode, func(t *testing.T) {
			fields := map[string]any{}
			for k, v := range base {
				fields[k] = v
			}
			if mode == "raw" {
				fields["origin"] = "raw"
			}
			if mode == "bad_identity" {
				fields["import_id"] = "not-an-id"
			}
			if mode == "zero_revision" {
				fields["revision"] = 0
			}
			metadata, _ := json.Marshal(map[string]any{"memlane_import_v1": fields})
			content := records.OfficialDocumentText{Document: records.OfficialDocument{Metadata: metadata}, Text: text}
			if mode == "wrong_body" {
				content.Text += " modified"
			}
			if importedKnowledge(content) != (mode == "knowledge") {
				t.Fatal("incorrect import classification", mode)
			}
			if content.SessionReference != "" || LearningBody(content) != content.Text {
				t.Fatal("import invented a session or stripped literal user content")
			}
		})
	}
}
