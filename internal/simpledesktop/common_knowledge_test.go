package simpledesktop

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
	body, _ := draft.markdown()
	outcome := ""
	kind := "pipeline"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Error("confirmation must not submit content")
		}
		switch r.URL.Path {
		case "/api/v1/datasets/common/data":
			_ = json.NewEncoder(w).Encode([]Document{{ID: "document", Dataset: "common", ContentHash: "hash", Metadata: map[string]any{"memlane_kind": "common_knowledge", "category": "preference"}}})
		case "/api/v1/datasets/common/data/document/raw":
			_, _ = w.Write([]byte(strings.TrimSuffix(body, "\n")))
		case "/api/v1/activity/pipeline-runs":
			if r.URL.Query().Get("dataset_id") != "common" {
				t.Error("unscoped pipeline check")
			}
			_ = json.NewEncoder(w).Encode([]map[string]string{{"kind": kind, "dataset_id": "common", "pipeline_run_id": "original-run", "status": "STARTED", "outcome": outcome}})
		default:
			t.Error(r.URL.Path)
		}
	}))
	defer server.Close()
	c := Client{Config: Config{ServiceURL: server.URL}}
	receipt := CommonWriteReceipt{Status: "running", DatasetID: "common", ContentHash: "hash", PipelineRunID: "original-run"}
	d, message, err := c.confirmCommon(context.Background(), Project{Dataset: "common"}, receipt, draft)
	if err != nil || d.ID != "document" || !strings.Contains(message, "正文已核对") || strings.Contains(message, "提炼已完成") {
		t.Fatalf("running confused with completion: %s %v", message, err)
	}
	outcome = "succeeded"
	kind = "operation"
	_, message, err = c.confirmCommon(context.Background(), Project{Dataset: "common"}, receipt, draft)
	if err != nil || strings.Contains(message, "提炼已完成") {
		t.Fatalf("background launch confused with processing: %s %v", message, err)
	}
	kind = "pipeline"
	_, message, err = c.confirmCommon(context.Background(), Project{Dataset: "common"}, receipt, draft)
	if err != nil || !strings.Contains(message, "提炼已完成") {
		t.Fatalf("terminal result missing: %s %v", message, err)
	}
}

func TestCommonEditorChangedSourceVersionNeverSubmitsPatch(t *testing.T) {
	var writes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			writes.Add(1)
		}
		_, _ = w.Write([]byte("# Changed by another author\n"))
	}))
	defer server.Close()
	a := New(Config{ServiceURL: server.URL}, nil)
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
	body, _ := original.markdown()
	d := Document{Metadata: map[string]any{"memlane_kind": "common_knowledge", "category": "preference", "source": original.Source}}
	parsed := commonDraftFor(Item{Body: body, BodyLoaded: true, Document: &d})
	roundtrip, err := parsed.markdown()
	if err != nil || roundtrip != body {
		t.Fatalf("edit duplicated/lost source or applicability: %q %v", roundtrip, err)
	}
}

func TestCommonKnowledgeOfficialMultipartAndPartialReceipt(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Header.Get("X-Api-Key") != "synthetic-owner-key" || r.Header.Get("Authorization") != "" {
			t.Error("wrong official credential protocol")
		}
		if err := r.ParseMultipartForm(1024 * 1024); err != nil {
			t.Fatal(err)
		}
		if r.FormValue("session_id") != "" || r.FormValue("content_type") != "" {
			t.Error("common knowledge was disguised as a session/type-specific ingest")
		}
		switch r.Method {
		case "POST":
			if r.FormValue("run_in_background") != "false" {
				t.Error("manual save needs the actual terminal receipt, not an uncorrelated background launch")
			}
			if r.URL.Path != "/api/v1/remember" || r.FormValue("datasetId") != "common-dataset" || r.FormValue("node_set") != "memlane_common" {
				t.Error("wrong common dataset/form protocol")
			}
			if !strings.Contains(r.FormValue("raw_data"), "# 标题\n\n正文") {
				t.Error("missing Markdown body")
			}
			var metadata []map[string]string
			if json.Unmarshal([]byte(r.FormValue("external_metadata")), &metadata) != nil || len(metadata) != 1 || metadata[0]["memlane_kind"] != "common_knowledge" || metadata[0]["category"] != "preference" {
				t.Error("external metadata is not a paired JSON array")
			}
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"status":"errored","dataset_id":"common-dataset","pipeline_run_id":"original-pipeline"}`))
		case "PATCH":
			if r.URL.Path != "/api/v1/update" || r.URL.Query().Get("dataset_id") != "common-dataset" || r.URL.Query().Get("data_id") != "document" {
				t.Error("wrong official update identity")
			}
			if _, ok := r.MultipartForm.Value["node_set"]; ok {
				t.Error("update must retain official node_set")
			}
			if _, ok := r.MultipartForm.Value["external_metadata"]; ok {
				t.Error("update must retain official metadata")
			}
			if len(r.MultipartForm.File["data"]) != 1 {
				t.Error("update must upload data file")
			}
			_, _ = w.Write([]byte(`{"status":"incremental","dataset_id":"common-dataset","data_id":"document","pipeline_run_id":"update-pipeline"}`))
		}
	}))
	defer server.Close()
	credential := filepath.Join(t.TempDir(), "official.token")
	if err := os.WriteFile(credential, []byte("synthetic-owner-key"), 0600); err != nil {
		t.Fatal(err)
	}
	client := Client{Config: Config{ServiceURL: server.URL, TokenFile: credential}}
	p := Project{Dataset: "common-dataset"}
	draft := CommonDraft{Title: "标题", Body: "正文", Category: "preference"}
	receipt, err := client.writeCommon(context.Background(), p, draft, nil)
	if err == nil || receipt.Status != "errored" || receipt.PipelineRunID != "original-pipeline" || requests != 1 {
		t.Fatalf("partial write identity lost or retried: %#v %v count=%d", receipt, err, requests)
	}
	document := Document{ID: "document", Dataset: p.Dataset, Name: "common.md", Metadata: map[string]any{"memlane_kind": "common_knowledge", "category": "preference"}}
	receipt, err = client.writeCommon(context.Background(), p, draft, &document)
	if err != nil || receipt.Status != "incremental" || receipt.PipelineRunID != "update-pipeline" {
		t.Fatalf("update result: %#v %v", receipt, err)
	}
	draft.Category = "experience"
	if _, err = client.writeCommon(context.Background(), p, draft, &document); err == nil || requests != 2 {
		t.Fatal("category change must be rejected before HTTP")
	}
}

func TestCommonDeleteRequiresVerifiedOfficialReceipt(t *testing.T) {
	remaining := true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "DELETE" || r.URL.Path != "/api/v1/datasets/common/data/document" {
			t.Error("wrong deletion endpoint")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "datasetId": "common", "dataId": "document", "dataRemaining": remaining})
	}))
	defer server.Close()
	client := Client{Config: Config{ServiceURL: server.URL}}
	d := Document{ID: "document", Dataset: "common", Metadata: map[string]any{"memlane_kind": "common_knowledge", "category": "experience"}}
	if err := client.deleteCommon(context.Background(), Project{Dataset: "common"}, d); err == nil {
		t.Fatal("HTTP 200 with dataRemaining=true cannot prove deletion")
	}
	remaining = false
	if err := client.deleteCommon(context.Background(), Project{Dataset: "common"}, d); err != nil {
		t.Fatal(err)
	}
}
