package desktop

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
)

func graphSubmissionFixture(t *testing.T, handler func(serviceipc.Request) serviceipc.Response) (*App, *GraphSubmissionUI, chan func()) {
	a, updates := experienceUIFixture(t, handler)
	project, identity, dataset, conn := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	a.page, a.scope = "项目", project
	a.guidanceRoot = t.TempDir()
	a.scopes = []records.Scope{{ID: project, Kind: "project", Name: "当前项目"}}
	a.projects = []ProjectItem{{ID: project, Name: "当前项目"}}
	s := &GraphSubmissionUI{Project: project, Identity: identity, IntentRoot: a.guidanceRoot, Kind: "knowledge", Loaded: true, RecoveryChecked: true, Chosen: map[string]records.Version{}, ConnectionID: conn}
	s.Connections = []records.Connection{{ID: conn, Agent: "pi", Name: "pi", Enabled: true, Generation: 2, AllowedScopes: []string{project}}}
	s.Binding = &records.CogneeBinding{MaterialSubmission: true, Project: project, Dataset: dataset, ConnectionID: conn, Generation: 2}
	s.Processing = records.ProcessingConfiguration{Current: records.ProcessingSelection{Active: true, Generation: 4, Revision: &records.ProcessingRevision{ID: uuid.NewString(), Spec: records.ProcessingSpec{Mode: "provider", Provider: "synthetic", Model: "test-model"}}}}
	a.browsing.Submissions = map[string]*GraphSubmissionUI{project: s}
	a.browsing.Material = &GraphMaterialUI{Project: project, Open: true, Submission: s}
	t.Cleanup(a.retireGraphSubmissions)
	return a, s, updates
}
func TestGraphSubmissionTokenSelectionAndUnknownStartKeepExactDurableOperation(t *testing.T) {
	var starts, gets atomic.Int64
	var a *App
	var s *GraphSubmissionUI
	var updates chan func()
	a, s, updates = graphSubmissionFixture(t, func(r serviceipc.Request) serviceipc.Response {
		switch r.Op {
		case "cognee_submission_start":
			starts.Add(1)
			q := r.CogneeSubmission
			saved, err := graphIntent(context.Background(), s.IntentRoot, s.Identity, s.Project, "read", nil)
			if err != nil || saved == nil || records.MergeDigest(saved) != records.MergeDigest(q) {
				t.Error("Start preceded durable exact intent", err)
			}
			return serviceipc.Response{OK: false, Error: "synthetic lost receipt"}
		case "cognee_submission_get":
			gets.Add(1)
			return serviceipc.Response{OK: true, Result: records.CogneeSubmissionTask{Operation: r.CogneeSubmissionID, Project: s.Project, Dataset: s.Binding.Dataset, State: "running", Materials: 2}}
		default:
			t.Errorf("unexpected %s", r.Op)
			return serviceipc.Response{OK: false}
		}
	})
	raw, knowledge := records.Version{Memory: uuid.NewString(), Dataset: s.Project, Revision: 2, Origin: "raw", Title: "原文"}, records.Version{Memory: uuid.NewString(), Dataset: s.Project, Revision: 5, Origin: "knowledge", Title: "知识"}
	a.selectGraphSubmissionMaterial(s, knowledge, true)
	s.Kind = "raw"
	a.selectGraphSubmissionMaterial(s, raw, true)
	a.prepareGraphSubmission(s)
	a.startGraphSubmission(s)
	if starts.Load() != 0 || s.Request != nil {
		t.Fatal("missing Token consent started work")
	}
	s.TokenConfirmed = true
	a.startGraphSubmission(s)
	manualAwait(t, updates)
	manualAwait(t, updates)
	if starts.Load() != 1 || gets.Load() != 1 || s.Task == nil || len(s.Request.Sources) != 2 {
		t.Fatal("unknown response created new operation")
	}
	operation := s.Request.Operation
	a.browsing.Material.Open = false
	a.scope = uuid.NewString()
	if !s.Watching || s.Request.Operation != operation {
		t.Fatal("leaving panel discarded daemon operation")
	}
	prior, err := graphIntent(context.Background(), s.IntentRoot, s.Identity, s.Project, "read", nil)
	if err != nil || prior.Operation != operation {
		t.Fatal("operation not recoverable", err)
	}
}
func TestGraphSubmissionIntentFailureNeverStartsOrDropsRequest(t *testing.T) {
	var calls atomic.Int64
	a, s, updates := graphSubmissionFixture(t, func(r serviceipc.Request) serviceipc.Response { calls.Add(1); return serviceipc.Response{OK: false} })
	if err := os.Symlink(t.TempDir(), filepath.Join(s.IntentRoot, "graph-submissions")); err != nil {
		t.Fatal(err)
	}
	v := records.Version{Memory: uuid.NewString(), Dataset: s.Project, Revision: 1, Origin: "knowledge"}
	a.selectGraphSubmissionMaterial(s, v, true)
	a.prepareGraphSubmission(s)
	s.TokenConfirmed = true
	a.startGraphSubmission(s)
	manualAwait(t, updates)
	if calls.Load() != 0 || s.Request == nil || !s.IntentError || s.Busy {
		t.Fatal("failed local intent barrier permitted IPC or lost request")
	}
}
func TestGraphSubmissionReopenQueriesJournalOperationWithoutStartingOrUsingLatest(t *testing.T) {
	var starts atomic.Int64
	operation := uuid.NewString()
	var s *GraphSubmissionUI
	a, state, updates := graphSubmissionFixture(t, func(r serviceipc.Request) serviceipc.Response {
		if r.Op != "cognee_submission_get" || r.CogneeSubmissionID != operation {
			starts.Add(1)
			return serviceipc.Response{OK: false}
		}
		return serviceipc.Response{OK: true, Result: records.CogneeSubmissionTask{Project: s.Project, Dataset: s.Binding.Dataset, Operation: operation, State: "cancelled"}}
	})
	s = state
	q := &records.CogneeSubmissionRequest{Operation: operation, Project: s.Project, Dataset: s.Binding.Dataset, Sources: []records.MergeSource{{Memory: uuid.NewString(), Revision: 3}}, Processing: records.ProcessingTaskAuthorization{Revision: uuid.NewString(), Generation: 1}}
	if _, err := graphIntent(context.Background(), s.IntentRoot, s.Identity, s.Project, "save", q); err != nil {
		t.Fatal(err)
	}
	saved, err := graphIntent(context.Background(), s.IntentRoot, s.Identity, s.Project, "read", nil)
	if err != nil {
		t.Fatal(err)
	}
	s.Request = saved
	s.RecoveryChecked = false
	a.recoverGraphSubmission(s)
	manualAwait(t, updates)
	if starts.Load() != 0 || s.Task == nil || s.Task.Operation != operation || s.Task.State != "cancelled" {
		t.Fatal("reopen restarted or guessed latest")
	}
}
func TestGraphSubmissionCancelWaitsForActualCleanupAndRejectsLateLibrary(t *testing.T) {
	var s *GraphSubmissionUI
	a, state, updates := graphSubmissionFixture(t, func(r serviceipc.Request) serviceipc.Response {
		return serviceipc.Response{OK: true, Result: records.CogneeSubmissionTask{Project: s.Project, Dataset: s.Binding.Dataset, Operation: r.CogneeSubmissionID, State: "running", CancelRequested: true}}
	})
	s = state
	s.Task = &records.CogneeSubmissionTask{Project: s.Project, Dataset: s.Binding.Dataset, Operation: uuid.NewString(), State: "running"}
	a.readGraphSubmission(s, true)
	manualAwait(t, updates)
	if graphSubmissionTerminal(s.Task.State) || !s.Task.CancelRequested {
		t.Fatal("cancel request claimed cleanup complete")
	}
	a.retireGraphSubmissions()
	a.libraryEpoch++
	old := s
	s = &GraphSubmissionUI{Project: old.Project, Message: "new library"}
	a.browsing.Submissions = map[string]*GraphSubmissionUI{s.Project: s}
	a.adoptGraphSubmissionTask(old, records.CogneeSubmissionTask{Project: old.Project, Dataset: old.Binding.Dataset, Operation: old.Task.Operation, State: "completed"}, old.Task.Operation, a.libraryEpoch-1)
	if s.Message != "new library" || s.Task != nil {
		t.Fatal("old task contaminated current library")
	}
}
func TestGraphSubmissionPanel800KeepsFooterAndDoesNotAutoSelect(t *testing.T) {
	a, s, _ := graphSubmissionFixture(t, func(serviceipc.Request) serviceipc.Response { return serviceipc.Response{OK: false} })
	for n := 0; n < 30; n++ {
		s.Items = append(s.Items, records.Version{Memory: uuid.NewString(), Dataset: s.Project, Origin: "knowledge", Revision: 1, Title: "当前项目知识"})
	}
	s.Total = 80
	s.Next = "real-next"
	tt := ui.NewTester(a.graphMaterialPanel, 388, 640)
	writeScreenshot(t, tt, "graph-submission-800-body.png")
	footer, ok := tt.Find("核对所选材料")
	if !ok || footer.Y+footer.H > 640 || footer.Y < 560 {
		t.Fatal("fixed footer outside narrow viewport", footer)
	}
	s.Scroll.Y = s.Scroll.MaxY
	tt.Frame()
	next, ok := tt.Find("下一页材料")
	if !ok || next.Y+next.H > 600 {
		t.Fatal("real pagination inaccessible at scroll bottom", next)
	}
	writeScreenshot(t, tt, "graph-submission-800-bottom.png")
	s.Items = s.Items[:1]
	a.selectGraphSubmissionMaterial(s, s.Items[0], true)
	a.prepareGraphSubmission(s)
	s.Scroll.Y = s.Scroll.MaxY
	tt.Frame()
	s.Scroll.Y = s.Scroll.MaxY
	tt.Frame()
	if confirm, ok := tt.Find("确认本次范围并接受 Token 消耗"); !ok || confirm.Y+confirm.H > 600 {
		t.Fatal("Token confirmation unreachable", confirm)
	}
	writeScreenshot(t, tt, "graph-submission-800-confirm.png")

	if len(s.Chosen) != 1 {
		t.Fatal("explicit choice changed unexpectedly")
	}
	if !tt.HasText("确认并开始处理") || !tt.HasText("下一页材料") {
		t.Fatal("narrow panel lost footer/pagination or selected automatically")
	}
}
func TestGraphSubmissionIntentIsPrivateBoundedAndEpochSeparated(t *testing.T) {
	root, identity, project := t.TempDir(), uuid.NewString(), uuid.NewString()
	q := &records.CogneeSubmissionRequest{Operation: uuid.NewString(), Project: project, Dataset: uuid.NewString(), Sources: []records.MergeSource{{Memory: uuid.NewString(), Revision: 1}}, Processing: records.ProcessingTaskAuthorization{Revision: uuid.NewString(), Generation: 1}}
	if _, err := graphIntent(context.Background(), root, identity, project, "save", q); err != nil {
		t.Fatal(err)
	}
	if other, err := graphIntent(context.Background(), root, uuid.NewString(), project, "read", nil); err != nil || other != nil {
		t.Fatal("new epoch adopted prior operation", err)
	}
	other := *q
	other.Operation = uuid.NewString()
	if _, err := graphIntent(context.Background(), root, identity, project, "save", &other); err == nil {
		t.Fatal("unresolved operation overwritten")
	}
	path := filepath.Join(root, "graph-submissions", identity+"-"+project+".json")
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := graphIntent(context.Background(), root, identity, project, "read", nil); err == nil {
		t.Fatal("non-private intent read")
	}
}

func TestGraphSubmissionRealIPCListUsesProjectKindFilterCursorAndNoAutoSelection(t *testing.T) {
	var s *GraphSubmissionUI
	var queries atomic.Int64
	a, state, updates := graphSubmissionFixture(t, func(r serviceipc.Request) serviceipc.Response {
		switch r.Op {
		case "list":
			queries.Add(1)
			q := r.ListQuery
			if len(q.Datasets) != 1 || q.Datasets[0] != s.Project || q.Kind != "raw" || q.Text != "确切搜索" || q.Source != "原始来源" || q.Cursor != "cursor-page-2" || q.Limit != 30 {
				t.Error("list scope/filter/pagination changed", q)
			}
			return serviceipc.Response{OK: true, Result: records.Page{Items: []records.Version{{Memory: uuid.NewString(), Dataset: s.Project, Revision: 7, Origin: "raw"}, {Memory: uuid.NewString(), Dataset: uuid.NewString(), Revision: 1, Origin: "raw"}, {Memory: uuid.NewString(), Dataset: s.Project, Revision: 1, Origin: "knowledge"}}, Total: 42, NextCursor: "cursor-page-3", Sources: []string{"原始来源"}}}
		case "processing_get":
			return serviceipc.Response{OK: true, Result: s.Processing}
		case "connections":
			return serviceipc.Response{OK: true, Result: s.Connections}
		case "cognee_bindings":
			return serviceipc.Response{OK: true, Result: []records.CogneeBinding{*s.Binding}}
		case "organization_preview_get":
			return serviceipc.Response{OK: true, Result: records.OrganizationPreviewState{Identity: s.Identity}}
		default:
			t.Errorf("unexpected op %s", r.Op)
			return serviceipc.Response{OK: false}
		}
	})
	s = state
	s.Kind = "raw"
	s.Filter = "确切搜索"
	s.Source = "原始来源"
	s.Cursor = "cursor-page-2"
	a.loadGraphSubmission(s)
	manualAwait(t, updates)
	if queries.Load() != 1 || len(s.Items) != 1 || len(s.Chosen) != 0 || s.Next != "cursor-page-3" || s.Items[0].Origin != "raw" {
		t.Fatal("foreign/mixed material or automatic selection")
	}
}
