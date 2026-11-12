package desktop

import (
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
)

func TestOfficialKnowledgeDefaultDoesNotHideLocalKnowledgeOrDrafts(t *testing.T) {
	for _, mode := range []string{"default", "local_knowledge", "unknown_count", "filtered", "draft", "submission_only", "no_binding", "late_navigation", "explicit_return"} {
		t.Run(mode, func(t *testing.T) {
			project := uuid.NewString()
			binding := records.CogneeBinding{Project: project, Instance: uuid.NewString(), Dataset: uuid.NewString(), ConnectionID: uuid.NewString(), Generation: 1}
			binding.MaterialSubmission = mode == "submission_only"
			zero := 0
			page := records.Page{KnowledgeTotal: &zero, RawTotal: &zero}
			if mode == "unknown_count" {
				page.KnowledgeTotal = nil
			}
			if mode == "local_knowledge" {
				one := 1
				page.KnowledgeTotal = &one
				page.Total = 1
				page.Items = []records.Version{{Memory: uuid.NewString(), Dataset: project, Title: "Retained local knowledge", Origin: "knowledge", State: "active"}}
			}
			row := records.OfficialDocument{ID: uuid.NewString(), Dataset: binding.Dataset, Name: "Stored lesson", MIMEType: "text/plain"}
			lesson := records.OfficialKnowledge{Content: records.OfficialDocumentText{Document: row, Text: "Stored official knowledge", SessionReference: "source-session"}, Title: "Stored lesson", ProcessingState: "completed"}
			var reads atomic.Int32
			a, dispatch := processingUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
				var result any
				switch r.Op {
				case "scopes":
					result = []records.Scope{{ID: project, Name: "Project", Kind: "project"}}
				case "list":
					result = page
				case "activities":
					result = records.ActivitySummary{}
				case "cognee_bindings":
					result = []records.CogneeBinding{binding}
					if mode == "no_binding" {
						result = []records.CogneeBinding{}
					}
				case "official_knowledge":
					reads.Add(1)
					result = records.OfficialKnowledgePage{Items: []records.OfficialKnowledge{lesson}, Limit: 20}
				case "official_knowledge_text":
					result = lesson
				default:
					return serviceipc.Response{Code: "unsupported"}
				}
				return serviceipc.Response{OK: true, Result: result}
			})
			a.page, a.scope, a.recordKind, a.detailMode = "项目", project, "knowledge", "view"
			a.browsing.View = "memories"
			if mode == "filtered" {
				a.readingFilter = "narrow result"
			}
			if mode == "draft" {
				a.editing = true
				a.detailMode = "new"
				a.draft = "unsaved draft"
			}
			a.refresh(false)
			if mode == "late_navigation" {
				a.loadSequence++
				a.scope = uuid.NewString()
			}
			awaitProcessingDispatch(t, dispatch)
			want := mode == "default" || mode == "explicit_return"
			if a.officialKnowledgeVisible() != want {
				t.Fatal("unexpected default route", mode)
			}
			if !want {
				if reads.Load() != 0 {
					t.Fatal("unapproved official knowledge requested")
				}
				if mode == "draft" && a.draft != "unsaved draft" {
					t.Fatal("draft lost")
				}
				if mode == "local_knowledge" && (len(a.items) != 1 || a.items[0].Title != "Retained local knowledge") {
					t.Fatal("unmigrated knowledge hidden")
				}
				return
			}
			awaitProcessingDispatch(t, dispatch)
			awaitProcessingDispatch(t, dispatch)
			if reads.Load() != 1 || a.conversations.Documents.Selected != row.ID {
				t.Fatal("default knowledge not read through normal source path")
			}
			if mode == "explicit_return" {
				a.closeOfficialKnowledge()
				awaitProcessingDispatch(t, dispatch)
				if a.officialKnowledgeVisible() || a.officialKnowledgeBypass != project || reads.Load() != 1 {
					t.Fatal("explicit return redirected in a loop")
				}
			}
		})
	}
}
