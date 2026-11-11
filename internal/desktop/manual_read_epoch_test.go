package desktop

import (
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
)

func TestManualDraftRestorationRetiresEarlierPageReadWithoutLosingEdits(t *testing.T) {
	dataset, identity, oldMemory, editedMemory := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	entered, release := make(chan struct{}), make(chan struct{})
	var lists atomic.Int64
	a, updates := experienceUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
		switch r.Op {
		case "scopes":
			return serviceipc.Response{OK: true, Result: []records.Scope{{ID: dataset, Kind: "project", Name: "current project"}}}
		case "list":
			if lists.Add(1) == 1 {
				close(entered)
				<-release
			}
			return serviceipc.Response{OK: true, Result: records.Page{Items: []records.Version{}, Total: 0}}
		case "activities":
			return serviceipc.Response{OK: true, Result: records.ActivitySummary{Days: map[string]int{}}}
		case "get":
			return serviceipc.Response{OK: true, Result: records.Version{Memory: r.Memory, Dataset: dataset, Revision: 2, Text: "后台更新后的有效正文", Origin: "knowledge", State: "active"}}
		case "history":
			return serviceipc.Response{OK: true, Result: records.HistoryPage{}}
		case "merge_list":
			return serviceipc.Response{OK: true, Result: records.MergePage{}}
		case "organization_sources":
			return serviceipc.Response{OK: true, Result: []records.OrganizationSource{}}
		default:
			t.Errorf("page read invoked %s", r.Op)
			return serviceipc.Response{OK: false}
		}
	})
	root := t.TempDir()
	loc := manualDraftLocation{Page: "项目", Scope: dataset, Selected: editedMemory, Mode: "edit", Kind: "knowledge", Scroll: 76}
	e := manualDraftEnvelope{Format: manualDraftFormat, Identity: identity, Entries: map[string]manualDraftEntry{editedMemory: {Dataset: dataset, Revision: 1, Title: "未提交标题", Text: "磁盘草稿正文", Location: loc}}, Active: &loc}
	writeManualFixture(t, root, e)
	a.configureManualDrafts(root)
	t.Cleanup(a.retireManualDrafts)
	a.scopes = []records.Scope{{ID: dataset, Kind: "project", Name: "current project"}}
	a.manualScopesLoaded()
	a.page, a.scope = "项目", dataset
	a.selectedID = oldMemory
	a.selectedItem = nil
	a.merges.Client = serviceipc.MergeClient{Root: a.library.Root}
	a.visualFixture = false
	a.refresh(false)
	<-entered
	a.loadManualDrafts(identity)
	manualAwait(t, updates)
	if a.selectedID != editedMemory || a.draft != "磁盘草稿正文" || !a.editing {
		t.Fatal("manual editor was not restored")
	}
	a.draft = "恢复后继续输入的正文"
	close(release)
	manualAwait(t, updates)
	manualAwait(t, updates)
	if a.selectedID != editedMemory || a.draft != "恢复后继续输入的正文" || a.drafts[editedMemory].Revision != 1 || !a.editing {
		t.Fatal("late library refresh replaced restored editor or advanced baseline")
	}
}

func TestLibraryReadFreezesAllClientsAndRejectsOldEpochBeforeUI(t *testing.T) {
	oldScope, newScope := uuid.NewString(), uuid.NewString()
	entered, release := make(chan struct{}), make(chan struct{})
	var newCalls atomic.Int64
	next, _ := experienceUIFixture(t, func(serviceipc.Request) serviceipc.Response { newCalls.Add(1); return serviceipc.Response{OK: false} })
	a, updates := experienceUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
		switch r.Op {
		case "scopes":
			close(entered)
			<-release
			return serviceipc.Response{OK: true, Result: []records.Scope{{ID: oldScope, Kind: "project", Name: "old"}}}
		case "list":
			return serviceipc.Response{OK: true, Result: records.Page{}}
		case "activities":
			return serviceipc.Response{OK: true, Result: records.ActivitySummary{}}
		default:
			t.Errorf("old library unexpected %s", r.Op)
			return serviceipc.Response{OK: false}
		}
	})
	a.visualFixture = false
	a.page, a.scope = "项目", oldScope
	a.selectedID = ""
	a.selectedItem = nil
	a.refresh(false)
	<-entered
	a.libraryEpoch++
	a.library = next.library
	a.libraryContext = next.libraryContext
	a.scope = newScope
	a.scopes = []records.Scope{{ID: newScope, Kind: "project", Name: "new"}}
	a.loading = true
	a.draft = "new epoch draft"
	close(release)
	manualAwait(t, updates)
	if newCalls.Load() != 0 || a.scope != newScope || a.scopes[0].ID != newScope || !a.loading || a.draft != "new epoch draft" {
		t.Fatal("old read mixed clients or mutated new epoch UI")
	}
}

func TestProjectRegistrationReceiptCannotClearNewLibraryForm(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	a, updates := experienceUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
		if r.Op != "register_project" {
			t.Errorf("unexpected registration %s", r.Op)
		}
		close(entered)
		<-release
		return serviceipc.Response{OK: true, Result: records.Scope{ID: uuid.NewString(), Kind: "project", Name: "old"}}
	})
	a.projectName, a.projectPath = "old form", "/synthetic/old"
	a.page = "项目"
	a.projectCreation = ProjectCreationUI{Open: true, OwnerScope: a.scope, Root: a.library.Root, Epoch: a.libraryEpoch, CheckedPath: a.projectPath, Inspection: &records.ProjectInspection{State: "ready", Identity: "explicit-synthetic-inspection"}}
	a.registerProject()
	<-entered
	a.libraryEpoch++
	a.projectName, a.projectPath = "new form", "/synthetic/new"
	a.projectBusy = true
	a.scope = "new-scope"
	close(release)
	manualAwait(t, updates)
	if a.projectName != "new form" || a.projectPath != "/synthetic/new" || !a.projectBusy || a.scope != "new-scope" {
		t.Fatal("old project receipt cleared or navigated new library form")
	}
}
