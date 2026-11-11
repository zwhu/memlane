package desktop

import (
	"testing"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
)

// A library restore can retain the same service path and App lifetime. A late
// old receipt must not retire the replacement library's draft or save owner.
func TestMutationReceiptCannotChangeRetiredLibrary(t *testing.T) {
	for _, outcome := range []string{"success", "failure", "conflict"} {
		t.Run(outcome, func(t *testing.T) {
			memory, dataset := uuid.NewString(), uuid.NewString()
			a, dispatched := processingUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
				if r.Op == "get" {
					return serviceipc.Response{OK: true, Result: records.Version{Memory: memory, Dataset: dataset, Revision: 2}}
				}
				if r.Op != "apply" || r.Mutation.Memory != memory {
					return serviceipc.Response{Code: "invalid_request", Error: "unexpected request"}
				}
				if outcome != "success" {
					code := "unavailable"
					if outcome == "conflict" {
						code = "conflict"
					}
					return serviceipc.Response{Code: code, Error: "old library response"}
				}
				return serviceipc.Response{OK: true, Result: records.Version{Memory: memory, Dataset: dataset, Revision: 1}}
			})
			a.page, a.scope, a.detailMode = "项目", dataset, "edit"
			a.applyMutation(memory, records.Mutation{OperationID: uuid.NewString(), Kind: "edit", Memory: memory, Dataset: dataset, ExpectedRevision: 1})
			// Retire the old library before dispatching its received IPC result.
			a.libraryEpoch++
			a.saving, a.editing = true, true
			a.message = "replacement library owns this save"
			a.pendingMutations[memory] = records.Mutation{OperationID: "replacement-operation"}
			a.drafts[memory] = Draft{Title: "replacement draft", Text: "preserve", Revision: 9}
			a.selectedItem = &MemoryItem{ID: memory, Revision: 9}
			awaitProcessingDispatch(t, dispatched)
			if !a.saving || !a.editing || a.detailMode != "edit" || a.message != "replacement library owns this save" || a.selectedItem.Revision != 9 {
				t.Fatal("old mutation receipt replaced the new library UI or save owner")
			}
			if a.pendingMutations[memory].OperationID != "replacement-operation" || a.drafts[memory].Text != "preserve" || a.conflictCurrent != nil {
				t.Fatal("old mutation receipt modified the new library draft or intent")
			}
		})
	}
}

func TestReadingReceiptsCannotChangeRetiredLibrary(t *testing.T) {
	for _, action := range []string{"search", "retry_index"} {
		t.Run(action, func(t *testing.T) {
			memory, dataset := uuid.NewString(), uuid.NewString()
			a, dispatched := processingUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
				if action == "search" && r.Op == "library_search" {
					return serviceipc.Response{OK: true, Result: []records.SearchHit{{Version: records.Version{Memory: memory, Dataset: dataset, Revision: 1, Text: "old library"}}}}
				}
				if action == "retry_index" && r.Op == action {
					return serviceipc.Response{OK: true}
				}
				return serviceipc.Response{Error: "unexpected request"}
			})
			a.page, a.scope, a.selectedID = "项目", dataset, memory
			a.selectedItem = &MemoryItem{ID: memory, Revision: 9}
			if action == "search" {
				a.query = "same query"
				a.searchMemory()
			} else {
				a.retryIndex()
			}
			a.libraryEpoch++
			a.message = "replacement library state"
			a.searchHits = []MemoryItem{{ID: "replacement", Text: "keep current results"}}
			a.searching, a.retryingIndex = true, true
			awaitProcessingDispatch(t, dispatched)
			if a.message != "replacement library state" || !a.searching || !a.retryingIndex || len(a.searchHits) != 1 || a.searchHits[0].ID != "replacement" {
				t.Fatal("old library search/index receipt changed replacement state")
			}
		})
	}
}
