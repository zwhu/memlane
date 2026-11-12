package official

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/zwhu/memlane/internal/records"
)

func TestOfficialDocumentActualService(t *testing.T) {
	path := os.Getenv("MEMLANE_DOCUMENT_PROBE")
	if path == "" {
		t.Skip("explicit synthetic service fixture required")
	}
	var probe struct {
		SyntheticOnly                         bool `json:"synthetic_only"`
		Endpoint, Instance, Dataset, Document string
		TokenFile                             string `json:"token_file"`
		ExpectedSHA256                        string `json:"expected_sha256"`
		Label                                 string
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(data, &probe); err != nil || !probe.SyntheticOnly || probe.Label != "memlane_contract_probe" || len(probe.ExpectedSHA256) != 64 {
		t.Fatal("synthetic fixture marker required")
	}
	key, err := os.ReadFile(probe.TokenFile)
	if err != nil {
		t.Fatal("private synthetic key unavailable")
	}
	r := DocumentReader{SessionReader{Endpoint: probe.Endpoint, Instance: probe.Instance, Dataset: probe.Dataset, Token: strings.TrimSpace(string(key))}}
	page, err := r.ListPage(context.Background(), 10, 0)
	if err != nil || len(page.Documents) != 1 || page.HasMore {
		t.Fatalf("synthetic catalog mismatch: count=%d error=%v", len(page.Documents), err)
	}
	d := page.Documents[0]
	if d.ID != probe.Document || d.Label == nil || *d.Label != probe.Label {
		t.Fatal("synthetic fixture identity mismatch")
	}
	text, err := r.ReadText(context.Background(), d, 0)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprintf("%x", sha256.Sum256([]byte(text.Text))) != probe.ExpectedSHA256 {
		t.Fatal("official raw text differs from uploaded synthetic payload")
	}
	if text.SessionReference != "" {
		t.Fatal("ordinary synthetic document was assigned a session learning reference")
	}
	t.Logf("actual official document read: dataset=%s document=%s text_sha256=%s", d.Dataset, d.ID, probe.ExpectedSHA256)
}

const documentTestInstance = "55dbbc14-3185-47e7-a870-547463c2ab5f"
const documentTestDataset = "39dac5b0-dca9-4efb-915d-ef572c81904c"

func documentTestRow(id string) records.OfficialDocument {
	return records.OfficialDocument{ID: id, Dataset: documentTestDataset, Name: "合成官方文档", MIMEType: "text/markdown", Metadata: json.RawMessage(`{"source":"synthetic"}`)}
}

func TestDocumentSessionReferenceRequiresConsistentMarkers(t *testing.T) {
	const body = "# Session learning (session source-session)\n\nA synthetic lesson.\n"
	for _, tt := range []struct{ name, metadata, text, want string }{
		{"matching", `{"node_set":["session_learnings","session_learnings:source-session"]}`, body, "source-session"},
		{"other tags", `{"node_set":["project-scope","session_learnings","session_learnings:source-session"]}`, body, "source-session"},
		{"header only", `{}`, body, ""},
		{"label only", `{"label":"session_learnings"}`, body, ""},
		{"missing general marker", `{"node_set":["session_learnings:source-session"]}`, body, ""},
		{"missing source marker", `{"node_set":["session_learnings"]}`, body, ""},
		{"body mismatch", `{"node_set":["session_learnings","session_learnings:another-session"]}`, body, ""},
		{"two sources", `{"node_set":["session_learnings","session_learnings:source-session","session_learnings:another-session"]}`, body, ""},
		{"wrong shape", `{"node_set":"session_learnings"}`, body, ""},
		{"bad entry", `{"node_set":["session_learnings",3]}`, body, ""},
		{"control in source", `{"node_set":["session_learnings","session_learnings:source\u0000session"]}`, body, ""},
		{"embedded header", `{"node_set":["session_learnings","session_learnings:source-session"]}`, "Quote:\n" + body, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := documentSessionReference(json.RawMessage(tt.metadata), tt.text); got != tt.want {
				t.Fatalf("reference %q, want %q", got, tt.want)
			}
		})
	}
}

func TestOfficialDocumentSessionChecksCurrentDocumentAndUniqueOwner(t *testing.T) {
	for _, mode := range []string{"read", "missing_reference", "expired", "ambiguous", "document_changed"} {
		t.Run(mode, func(t *testing.T) {
			selected := documentTestRow("e6a5ca85-2360-4d11-a4aa-78161f632096")
			selected.Metadata = json.RawMessage(`{"node_set":["session_learnings","session_learnings:source-session"]}`)
			if mode == "missing_reference" {
				selected.Metadata = nil
			}
			row := records.SessionSummary{Session: "source-session", User: "a6a5ca85-2360-4d11-a4aa-78161f632096", Dataset: documentTestDataset}
			sessionReads := 0
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
					current := selected
					if mode == "document_changed" && sessionReads > 0 {
						current.Name = "changed document"
					}
					json.NewEncoder(w).Encode([]records.OfficialDocument{current})
				case strings.HasSuffix(r.URL.Path, "/raw"):
					fmt.Fprint(w, "# Session learning (session source-session)\n\nA synthetic lesson.\n")
				case r.URL.Path == "/api/v1/sessions":
					rows := []records.SessionSummary{row}
					if mode == "expired" {
						rows = nil
					}
					if mode == "ambiguous" {
						other := row
						other.User = "b6a5ca85-2360-4d11-a4aa-78161f632096"
						rows = append(rows, other)
					}
					json.NewEncoder(w).Encode(records.SessionPage{Sessions: rows, Total: len(rows), Limit: 100})
				case r.URL.Path == "/api/v1/sessions/source-session":
					sessionReads++
					json.NewEncoder(w).Encode(records.SessionDetail{SessionSummary: row, QACount: 1, QAs: []records.SessionQA{{ID: "qa", Question: "synthetic question", Answer: "synthetic answer"}}})
				default:
					t.Error("unexpected route")
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			r := DocumentReader{SessionReader{Endpoint: server.URL, Instance: documentTestInstance, Dataset: documentTestDataset, Token: "synthetic"}}
			out, err := r.ReadSession(context.Background(), selected, 0)
			if mode == "read" {
				if err != nil || out.Document.ID != selected.ID || out.Detail.User != row.User {
					t.Fatal(out, err)
				}
			} else if err == nil || out.Document.ID != "" || len(out.Detail.QAs) != 0 {
				t.Fatal("unverified source returned", out, err)
			}
			if mode == "document_changed" && !errors.Is(err, ErrOfficialDocumentChanged) {
				t.Fatal(err)
			}
			if (mode == "missing_reference" || mode == "ambiguous" || mode == "expired") && sessionReads != 0 {
				t.Fatal("unsafe session detail request", sessionReads)
			}
			if mode == "expired" {
				text, e := r.ReadText(context.Background(), selected, 0)
				if e != nil || text.Text == "" {
					t.Fatal("expired session hid permanent document", e)
				}
			}
		})
	}
}

func TestOfficialDocumentsUseLookaheadAndKeepNullableMetadata(t *testing.T) {
	rows := []records.OfficialDocument{documentTestRow("e6a5ca85-2360-4d11-a4aa-78161f632096"), documentTestRow("a6a5ca85-2360-4d11-a4aa-78161f632096"), documentTestRow("b6a5ca85-2360-4d11-a4aa-78161f632096")}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.Header.Get("X-Api-Key") != "synthetic" {
			t.Error("write or wrong principal")
			w.WriteHeader(403)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/identity") {
			json.NewEncoder(w).Encode(map[string]string{"instance": documentTestInstance, "cognee_version": "1.6.7"})
			return
		}
		if r.URL.Query().Get("limit") != "3" || r.URL.Query().Get("offset") != "8" {
			t.Error("missing upstream pagination")
		}
		json.NewEncoder(w).Encode(rows)
	}))
	defer server.Close()
	r := DocumentReader{SessionReader{Endpoint: server.URL, Instance: documentTestInstance, Dataset: documentTestDataset, Token: "synthetic"}}
	page, err := r.ListPage(context.Background(), 2, 8)
	if err != nil || len(page.Documents) != 2 || !page.HasMore || page.Offset != 8 || page.Documents[0].CreatedAt != nil || page.Documents[0].ContentHash != nil {
		t.Fatalf("incorrect catalog/null mapping: %+v %v", page, err)
	}
}

func TestOfficialDocumentReadWithholdsContentOnChangeOrRevocation(t *testing.T) {
	for _, mode := range []string{"normal", "moved", "metadata_changed", "service_changed", "revoked", "wrong_dataset", "binary", "unsupported", "oversized"} {
		t.Run(mode, func(t *testing.T) {
			selected := documentTestRow("e6a5ca85-2360-4d11-a4aa-78161f632096")
			if mode == "unsupported" {
				selected.MIMEType = "application/pdf"
			}
			rawRead := false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.Header.Get("X-Api-Key") != "synthetic" {
					t.Error("write or wrong principal")
					w.WriteHeader(403)
					return
				}
				if strings.HasSuffix(r.URL.Path, "/identity") {
					if mode == "revoked" && rawRead {
						w.WriteHeader(403)
						return
					}
					instance := documentTestInstance
					if mode == "service_changed" && rawRead {
						instance = documentTestDataset
					}
					json.NewEncoder(w).Encode(map[string]string{"instance": instance, "cognee_version": "1.6.7"})
					return
				}
				if strings.HasSuffix(r.URL.Path, "/raw") {
					rawRead = true
					switch mode {
					case "binary":
						w.Write([]byte{0xff, 0})
					case "oversized":
						fmt.Fprint(w, strings.Repeat("x", 8*1024*1024+1))
					default:
						fmt.Fprint(w, "## 合成内容\n\n直接来自官方接口。")
					}
					return
				}
				if r.URL.Query().Get("content_hash") != "" {
					t.Error("owner-only hash filter loses ACL readers")
				}
				d := selected
				if mode == "wrong_dataset" {
					d.Dataset = documentTestInstance
				}
				if mode == "metadata_changed" && rawRead {
					d.Name = "新版本"
				}
				rows := []records.OfficialDocument{d}
				if mode == "moved" {
					rows = append([]records.OfficialDocument{documentTestRow("a6a5ca85-2360-4d11-a4aa-78161f632096")}, rows...)
				}
				json.NewEncoder(w).Encode(rows)
			}))
			defer server.Close()
			r := DocumentReader{SessionReader{Endpoint: server.URL, Instance: documentTestInstance, Dataset: documentTestDataset, Token: "synthetic"}}
			out, err := r.ReadText(context.Background(), selected, 0)
			if mode == "normal" || mode == "moved" {
				if err != nil || !strings.Contains(out.Text, "直接来自官方") {
					t.Fatal(out, err)
				}
			} else if err == nil || out.Text != "" {
				t.Fatal("unverified content leaked", out, err)
			}
			if mode == "metadata_changed" && !errors.Is(err, ErrOfficialDocumentChanged) {
				t.Fatal("lost changed-source identity", err)
			}
			if mode == "unsupported" && rawRead {
				t.Fatal("downloaded unsupported binary")
			}
			if (mode == "unsupported" || mode == "binary") && !errors.Is(err, ErrOfficialDocumentUnsupported) {
				t.Fatal("unsupported preview reported as a provenance/permission failure", err)
			}
		})
	}
}
