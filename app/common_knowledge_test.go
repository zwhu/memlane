package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestCommonNormalDocumentPresentationPreservesRawAndUsesTitle(t *testing.T) {
	raw := "# A common preference\n\nUse the applicable convention.\n\n## 来源\n\nManual verification"
	d := Document{ID: "common-doc", Dataset: "common", Name: "text_0123456789abcdef0123456789abcdef.txt", Metadata: map[string]any{"memlane_kind": "common_knowledge", "category": "preference", "source": "Manual verification"}}
	p := itemPresentation(Item{Kind: "知识", Document: &d, Body: raw})
	if p.DisplayTitle != "A common preference" || p.VisibleBody != raw || p.Unrecognized || p.WrapperRecognized {
		t.Fatalf("normal common document treated as a session wrapper: %#v", p)
	}
}

func TestCommonConfirmKeepsSavedBodyAndProcessingStatusSeparate(t *testing.T) {
	draft := CommonDraft{Title: "A preference", Body: "Use the project validation command.", Category: "preference"}
	body, _ := draft.FormatDocument()
	outcome := ""
	kind := "pipeline"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Error("confirmation must not submit content")
		}
		switch r.URL.Path {
		case "/v1/capabilities":
			json.NewEncoder(w).Encode(ControlCapabilities{ReadingContract: readingContract})
		case "/v1/common/knowledge":
			q := r.URL.Query()
			if q.Get("dataset_id") != "common" || q.Get("common_category") != "preference" || q.Get("limit") != "30" || q.Has("cursor") {
				t.Error("confirmation did not request one bounded category page", q)
			}
			json.NewEncoder(w).Encode(v2CommonKnowledgeResponse{Items: []v2KnowledgeItem{
				{DocID: "wrong-hash", DatasetID: "common", CommonCategory: "preference", ContentHash: "different"},
				{DocID: "document", DatasetID: "common", CommonCategory: "preference"},
			}})
		case "/api/knowledge/common/document/body":
			_ = json.NewEncoder(w).Encode(map[string]any{"dataset_id": "common", "document_id": "document", "content": strings.TrimSuffix(body, "\n"), "mime_type": "text/markdown"})
		case "/v1/activity/pipeline-runs":
			if r.URL.Query().Get("dataset_id") != "common" {
				t.Error("unscoped pipeline check")
			}
			_ = json.NewEncoder(w).Encode([]map[string]string{{"kind": kind, "dataset_id": "common", "pipeline_run_id": "original-run", "status": "STARTED", "outcome": outcome}})
		default:
			t.Error(r.URL.Path)
		}
	}))
	defer server.Close()
	ctrl := readingControlConfig(t, server.URL)
	c := Client{Config: Config{ProjectControl: ctrl}}
	control := ProjectControlClient{Config: *ctrl}
	receipt := CommonWriteReceipt{Status: "running", DatasetID: "common", ContentHash: "hash", PipelineRunID: "original-run"}
	d, message, err := c.confirmCommon(context.Background(), control, true, Project{Dataset: "common"}, receipt, draft)
	if err != nil || d.ID != "document" || !strings.Contains(message, "正文已核对") || strings.Contains(message, "提炼已完成") {
		t.Fatalf("running confused with completion: %s %v", message, err)
	}
	outcome = "succeeded"
	kind = "operation"
	_, message, err = c.confirmCommon(context.Background(), control, true, Project{Dataset: "common"}, receipt, draft)
	if err != nil || strings.Contains(message, "提炼已完成") {
		t.Fatalf("background launch confused with processing: %s %v", message, err)
	}
	kind = "pipeline"
	_, message, err = c.confirmCommon(context.Background(), control, true, Project{Dataset: "common"}, receipt, draft)
	if err != nil || !strings.Contains(message, "提炼已完成") {
		t.Fatalf("terminal result missing: %s %v", message, err)
	}
}

func TestCommonConfirmBoundsCandidateReadsAndFailsClosed(t *testing.T) {
	draft := CommonDraft{Title: "A preference", Body: "Keep internal  spacing.", Category: "preference"}
	expected, err := draft.FormatDocument()
	if err != nil {
		t.Fatal(err)
	}
	controlCfg := readingControlConfig(t, "http://127.0.0.1:12345")
	reads := 0
	httpClient := &http.Client{Transport: queueTestTransport(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/knowledge/") {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path == "/v1/common/knowledge" {
			items := []v2KnowledgeItem{
				{DocID: "foreign", DatasetID: "other", CommonCategory: "preference"},
				{DocID: "wrong-category", DatasetID: "common", CommonCategory: "experience"},
			}
			for _, id := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "match"} {
				items = append(items, v2KnowledgeItem{DocID: id, DatasetID: "common", CommonCategory: "preference"})
			}
			json.NewEncoder(w).Encode(v2CommonKnowledgeResponse{Items: items, HasMore: true, NextCursor: "unused"})
			return
		}
		id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/datasets/common/documents/"), "/body")
		if id == "" || !strings.HasSuffix(r.URL.Path, "/body") || id == "foreign" || id == "wrong-category" {
			t.Errorf("unexpected confirmation request %s", r.URL)
			http.NotFound(w, r)
			return
		}
		reads++
		body := expected
		if id != "match" {
			body = strings.Replace(expected, "internal  spacing", "internal spacing", 1)
		}
		json.NewEncoder(w).Encode(v2DocumentBodyResponse{ID: id, DatasetID: "common", Body: &body})
	})}
	client := Client{Config: Config{ProjectControl: controlCfg}, HTTP: httpClient}
	control := ProjectControlClient{Config: *controlCfg, HTTP: httpClient}
	project := Project{Dataset: "common"}
	doc, status, err := client.confirmCommon(context.Background(), control, true, project, CommonWriteReceipt{}, draft)
	if doc.ID != "" || status != "保存尚未核对" || err == nil || reads != 8 {
		t.Fatalf("confirmation should stop before the ninth body: doc=%q status=%q err=%v reads=%d", doc.ID, status, err, reads)
	}
	doc, status, err = client.confirmCommon(context.Background(), control, false, project, CommonWriteReceipt{}, draft)
	if doc.ID != "" || status != "保存尚未核对" || err == nil || !strings.Contains(err.Error(), "控制面服务不可用") || reads != 8 {
		t.Fatalf("missing control plane did not fail closed: doc=%q status=%q err=%v reads=%d", doc.ID, status, err, reads)
	}
}

func TestCommonEditorChangedSourceVersionNeverSubmitsPatch(t *testing.T) {
	var writes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			writes.Add(1)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"dataset_id":      "common",
			"doc_id":          "document",
			"document_id":     "document",
			"common_category": "preference",
			"content":         "# Changed by another author\n",
		})
	}))
	defer server.Close()
	a := New(Config{ProjectControl: readingControlConfig(t, server.URL)}, nil)
	updates := make(chan func(), 10)
	a.Update = func(fn func()) { updates <- fn }
	d := Document{ID: "document", Dataset: "common", Metadata: map[string]any{"memlane_kind": "common_knowledge", "category": "preference"}}
	a.commonEdit = commonEditorState{Open: true, Draft: CommonDraft{Title: "Updated title", Body: "New body", Category: "preference"}, Existing: &d, Target: Project{Dataset: "common"}, Baseline: sha256.Sum256([]byte("old body"))}
	a.saveCommonKnowledge()
	drainReading(t, updates, func() bool { return !a.commonEdit.Busy })
	if writes.Load() != 0 || a.commonEdit.Submitted || !strings.Contains(a.commonEdit.Message, "本次未提交") {
		t.Fatal("stale editor overwrote official content")
	}
}

func TestCommonDraftRoundtripRetainsSourceAndApplicability(t *testing.T) {
	original := CommonDraft{Title: "A preference", Body: "Use pnpm.\n\nKeep the lockfile.", Category: "preference", Source: "User's original note", Applicability: "JavaScript repositories"}
	body, _ := original.FormatDocument()
	d := Document{Metadata: map[string]any{"memlane_kind": "common_knowledge", "category": "preference", "source": original.Source}}
	parsed := commonDraftFor(Item{Body: body, BodyLoaded: true, Document: &d})
	roundtrip, err := parsed.FormatDocument()
	if err != nil || roundtrip != body {
		t.Fatalf("edit duplicated/lost source or applicability: %q %v", roundtrip, err)
	}
}

func TestCommonKnowledgeOfficialMultipartAndPartialReceipt(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") || r.Header.Get("X-Api-Key") != "" {
			t.Error("wrong gateway credential protocol")
		}
		switch r.Method {
		case "POST":
			if r.URL.Path != "/api/common" {
				t.Error("wrong common create endpoint", r.URL.Path)
			}
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			if payload["common_category"] != "preference" || !strings.Contains(payload["content"].(string), "# 标题\n\n正文") {
				t.Error("missing category or Markdown body", payload)
			}
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"error":"common_write_failed","message":"errored"}`))
		case "PATCH":
			if r.URL.Path != "/api/common/document" {
				t.Error("wrong common update endpoint", r.URL.Path)
			}
			_, _ = w.Write([]byte(`{"ok":true,"dataset_id":"common-dataset","document_id":"document","doc_id":"document"}`))
		}
	}))
	defer server.Close()
	client := Client{Config: Config{ProjectControl: readingControlConfig(t, server.URL)}}
	p := Project{Dataset: "common-dataset"}
	draft := CommonDraft{Title: "标题", Body: "正文", Category: "preference"}
	receipt, err := client.writeCommon(context.Background(), p, draft, nil)
	if err == nil || !receipt.OutcomeUnknown || requests != 1 {
		t.Fatalf("partial write identity lost or retried: %#v %v count=%d", receipt, err, requests)
	}
	document := Document{ID: "document", Dataset: p.Dataset, Name: "common.md", Metadata: map[string]any{"memlane_kind": "common_knowledge", "category": "preference"}}
	receipt, err = client.writeCommon(context.Background(), p, draft, &document)
	if err != nil || !receipt.Gateway || receipt.DataID != "document" || receipt.DatasetID != "common-dataset" {
		t.Fatalf("update result: %#v %v", receipt, err)
	}
	draft.Category = "experience"
	if _, err = client.writeCommon(context.Background(), p, draft, &document); err == nil || requests != 2 {
		t.Fatal("category change must be rejected before HTTP")
	}
}

func TestCommonDeleteRequiresVerifiedOfficialReceipt(t *testing.T) {
	fail := true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "DELETE" || r.URL.Path != "/api/common/document" {
			t.Error("wrong deletion endpoint", r.URL.Path)
		}
		if fail {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"delete_failed"}`))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	client := Client{Config: Config{ProjectControl: readingControlConfig(t, server.URL)}}
	d := Document{ID: "document", Dataset: "common", Metadata: map[string]any{"memlane_kind": "common_knowledge", "category": "experience"}}
	if err := client.deleteCommon(context.Background(), Project{Dataset: "common"}, d); err == nil {
		t.Fatal("HTTP 500 cannot prove deletion")
	}
	fail = false
	if err := client.deleteCommon(context.Background(), Project{Dataset: "common"}, d); err != nil {
		t.Fatal(err)
	}
}
