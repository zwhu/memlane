package desktop

import (
	"context"
	"github.com/zwhu/memlane/internal/engine"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
	"testing"
	"time"
)

func TestRankedSearchSnapshotSurvivesSaveAndRestoreCallbacks(t *testing.T) {
	for _, scenario := range []struct{ Kind, Page string }{{"save", "搜索"}, {"restore", "搜索"}, {"save", "项目"}, {"restore", "项目"}} {
		kind, route := scenario.Kind, scenario.Page
		t.Run(route+"/"+kind, func(t *testing.T) {
			stored := records.Version{Record: engine.Record{Memory: "new-knowledge", Dataset: "project-a", Revision: 1, Text: "Does not match the submitted query"}, Title: "Different title", Origin: "knowledge", Source: "手动新增", State: "active", IndexState: "pending", Operation: "save"}
			a, updates := processingUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
				var value any
				switch r.Op {
				case "apply":
					if r.Mutation.Kind != kind {
						t.Errorf("wrong mutation kind %s", r.Mutation.Kind)
					}
					if kind == "restore" {
						stored.Revision = 2
						stored.Operation = "restore"
					}
					value = stored
				case "scopes":
					value = []records.Scope{{ID: "project-a", Kind: "project", Name: "Project A"}}
				case "list":
					value = records.Page{Items: []records.Version{stored}, Total: 1} // Deliberately unrelated ordinary list.
				case "get":
					value = stored
				case "history":
					value = records.HistoryPage{}
				case "activities":
					value = records.ActivitySummary{}
				case "merge_list":
					value = records.MergePage{}
				case "organization_sources":
					value = []records.OrganizationSource{}
				default:
					return serviceipc.Response{Code: "unsupported", Error: "unused synthetic metadata"}
				}
				return serviceipc.Response{OK: true, Result: value}
			})
			root := a.library.Root
			life, cancel := context.WithCancel(context.Background())
			defer cancel()
			stopped, stop := context.WithCancel(context.Background())
			stop()
			a.ConfigureLibrary(stopped, root, func(fn func()) { updates <- fn })
			a.libraryContext = life
			a.manualDrafts.Loaded = true
			a.page = route
			a.scope = ""
			if route == "项目" {
				a.scope = "project-a"
			}
			a.memoryRoute = "搜索:"
			a.query = "HarborCourier"
			a.searchQuery = a.query
			a.recordKind = "raw"
			if route == "项目" {
				a.recordKind = "knowledge"
			}
			a.activityDate = "2026-11-11"
			a.readingSearch = "README"
			a.readingFilter = "来源"
			a.listTotal = 1
			a.nextCursor = "ranked-next"
			ranked := MemoryItem{ID: "ranked-raw", Dataset: "project-a", Revision: 1, Title: "HarborCourier", Text: "matching raw", Origin: "raw"}
			a.items = []MemoryItem{ranked}
			a.selectedID = ranked.ID
			a.selectedItem = &ranked
			a.detailMode = "view"
			if kind == "save" {
				a.newMemory()
				a.draftTitle = "Different title"
				a.draft = "Does not match the submitted query"
				// Exercise the real typed Apply completion callback; no draft persistence or live DB.
				a.applyMutation(a.newDraftKey(), records.Mutation{OperationID: "synthetic-save", Kind: "save", Memory: stored.Memory, Dataset: stored.Dataset, Text: a.draft, Title: a.draftTitle, Source: "手动新增"})
			} else {
				item := displayVersion(stored)
				a.selectedID = item.ID
				a.selectedItem = &item
				a.restoreExpected = 1
				a.onRestore(1) // ConfigureLibrary's actual restore callback.
			}
			deadline := time.After(5 * time.Second)
			for a.saving || a.loading {
				select {
				case fn := <-updates:
					fn()
				case <-deadline:
					t.Fatal("mutation/refresh callback absent")
				}
			}
			if a.page != route || a.query != "HarborCourier" || a.searchQuery != a.query || a.recordKind != map[string]string{"搜索": "raw", "项目": "knowledge"}[route] || a.activityDate != "2026-11-11" || a.readingSearch != "README" || a.readingFilter != "来源" {
				t.Fatal("mutation replaced search context")
			}
			if a.selectedItem == nil || a.selectedItem.ID != stored.Memory || a.selectedItem.Origin != "knowledge" {
				t.Fatal("saved knowledge misrepresented as raw or confirmation detail lost")
			}
			if route == "项目" {
				if len(a.items) != 1 || a.items[0].ID != stored.Memory || a.nextCursor != "" {
					t.Fatal("ordinary list refresh was suppressed")
				}
				return
			}
			if a.nextCursor != "ranked-next" {
				t.Fatal("ranked cursor cleared")
			}
			if len(a.items) != 1 || a.items[0].ID != ranked.ID {
				t.Fatalf("ranked snapshot lost/replaced after %s: %+v", kind, a.items)
			}
			if a.items[0].ID == stored.Memory {
				t.Fatal("unmatched saved record claimed as a search hit")
			}
		})
	}
}

func TestSearchMutationReceiptKeepsNewNavigationOwner(t *testing.T) {
	a, updates := processingUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
		if r.Op == "apply" {
			return serviceipc.Response{OK: true, Result: records.Version{Record: engine.Record{Memory: "old-saved", Dataset: "old-project", Revision: 1}, Origin: "knowledge"}}
		}
		return serviceipc.Response{Code: "unsupported", Error: "unused synthetic refresh"}
	})
	a.page, a.scope, a.detailMode = "搜索", "old-project", "new"
	a.query = "old"
	a.searchQuery = "old"
	a.applyMutation("new:old-project", records.Mutation{OperationID: "old-save", Kind: "save", Memory: "old-saved", Dataset: "old-project"})
	a.page, a.scope, a.detailMode = "搜索", "new-project", "view"
	a.query = "new"
	a.searchQuery = "new"
	a.nextCursor = "new-owner-next"
	owner := MemoryItem{ID: "new-owner", Dataset: "new-project", Origin: "raw", Revision: 4}
	a.items = []MemoryItem{owner}
	a.selectedID = owner.ID
	a.selectedItem = &owner
	awaitProcessingDispatch(t, updates)
	if a.selectedID != owner.ID || a.selectedItem.ID != owner.ID || len(a.items) != 1 || a.items[0].ID != owner.ID || a.nextCursor != "new-owner-next" || a.searchQuery != "new" {
		t.Fatal("late save receipt replaced current search owner")
	}
	if a.routeStates["搜索:old-project"].ID != "old-saved" {
		t.Fatal("original receipt lost its original route")
	}
}
