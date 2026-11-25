package reading

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"testing"

	"github.com/zwhu/memlane/app/api"
)

func TestCommonMemoryCRUDAndDraftProtection(t *testing.T) {
	draft := CommonDraft{Title: "Title", Body: "Body", Category: "preference", Source: "source", Applicability: "condition"}
	content, err := FormatDocument(draft)
	if err != nil || ParseDocument("", content, draft.Category, draft.Source) != draft ||
		!HasDraft(draft, false, false, false, false) || HasDraft(draft, true, true, true, true) {
		t.Fatalf("draft: %q %v", content, err)
	}
	stored, writes := "", 0
	client := readingTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "POST /api/common", "PATCH /api/common/doc":
			var req api.SaveCommonMemoryRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Content == nil ||
				(r.Method == http.MethodPatch && req.Category != nil) {
				t.Errorf("invalid save: %+v %v", req, err)
				return
			}
			stored = *req.Content
			writes++
			_ = json.NewEncoder(w).Encode(api.SaveCommonMemoryResponse{OK: true, DocumentID: "doc", Content: stored})
		case "GET /api/common":
			_ = json.NewEncoder(w).Encode(api.KnowledgePage{Items: []api.CatalogItem{{DocID: "doc"}}})
		case "GET /api/common/doc":
			_ = json.NewEncoder(w).Encode(api.DocumentDetail{Content: stored,
				CatalogItem: api.CatalogItem{DocID: "doc", CommonCategory: "preference", PipelineStatus: "running"}})
		case "DELETE /api/common/doc":
			stored = ""
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	})
	c := CommonController{Client: client}
	ctx := t.Context()
	if receipt, err := c.Save(ctx, "", draft, [32]byte{}); err != nil || !receipt.OK || receipt.Content != content {
		t.Fatalf("create: %+v %v", receipt, err)
	}
	if page, err := c.List(ctx, api.KnowledgePageOptions{}); err != nil || len(page.Items) != 1 {
		t.Fatalf("list: %+v %v", page, err)
	}
	item, err := c.Get(ctx, "doc")
	if err != nil || item.Content != content || ProcessingStatus(CommonSaveStatus(item.PipelineStatus)) != "进行中" {
		t.Fatalf("save and processing outcomes: %+v %v", item, err)
	}
	if _, err := c.Save(ctx, "doc", draft, BaselineSHA256("stale")); !errors.Is(err, ErrCommonChanged) || writes != 1 {
		t.Fatalf("stale baseline wrote: %v, writes=%d", err, writes)
	}
	draft.Body = "Edited"
	if _, err := c.Save(ctx, "doc", draft, BaselineSHA256(content)); err != nil || writes != 2 {
		t.Fatalf("update: %v, writes=%d", err, writes)
	}
	if err := c.Delete(ctx, "doc"); err != nil || stored != "" {
		t.Fatalf("delete: %v", err)
	}
}

func TestProfilesAPIVerbatimImportManualCancelAndDelete(t *testing.T) {
	const inner = "# Profile\n\n~~~go\nfunc example() {}\n~~~"
	body := "\n```markdown\n" + inner + "\n```\n"
	var calls []string
	client := readingTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		switch r.Method + " " + r.URL.Path {
		case "GET /api/profiles":
			_ = json.NewEncoder(w).Encode(api.ListUserProfilesResponse{Profiles: []api.UserProfileView{{Source: "codex", Name: "Codex", CanGenerate: true}}})
		case "GET /api/profiles/codex", "POST /api/profiles/codex/manual":
			if r.Method == http.MethodPost {
				var req map[string]string
				_ = json.NewDecoder(r.Body).Decode(&req)
				if req["body"] != body {
					t.Error("manual save modified verbatim Markdown")
				}
			}
			_ = json.NewEncoder(w).Encode(api.UserProfileView{Source: "codex", Body: body})
		case "POST /api/profiles/codex/import", "POST /api/profiles/codex/cancel", "DELETE /api/profiles/codex":
			_, _ = w.Write([]byte("{}"))
		default:
			http.NotFound(w, r)
		}
	})
	c := ProfilesController{Client: client}
	ctx := t.Context()
	list, err := c.List(ctx)
	if err != nil || len(list.Profiles) != 1 {
		t.Fatalf("list: %+v %v", list, err)
	}
	p, savedBody, err := c.Get(ctx, list.Profiles[0].ID)
	copied, ok := ProfileCopy(savedBody)
	if err != nil || !ok || copied != body || UnwrapExportEnvelope(savedBody) != inner {
		t.Fatalf("verbatim copy/unwrap: %q %v", savedBody, err)
	}
	if err := c.Act(ctx, p, "generate", "op", ""); err != nil {
		t.Fatal(err)
	}
	p.Status, p.Operation = "generating", "op"
	if err := c.Act(ctx, p, "cancel", "", ""); err != nil {
		t.Fatal(err)
	}
	p.Status = "saving"
	if err := c.Act(ctx, p, "cancel", "", ""); !errors.Is(err, ErrProfileSaving) {
		t.Fatalf("cancel while saving: %v", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := c.Act(cancelled, p, "import", "manual", body); err != nil {
		t.Fatalf("manual save was cancelled: %v", err)
	}
	p.Status = "ready"
	if err := c.Act(ctx, p, "delete", "", ""); err != nil {
		t.Fatal(err)
	}
	want := []string{"GET /api/profiles", "GET /api/profiles/codex", "POST /api/profiles/codex/import",
		"POST /api/profiles/codex/cancel", "POST /api/profiles/codex/manual", "DELETE /api/profiles/codex"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("profile calls: got %v want %v", calls, want)
	}
}
