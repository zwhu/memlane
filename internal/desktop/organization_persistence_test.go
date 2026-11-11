package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
)

func TestMaterialWriterKeepsDifferentTasksBarriersAndExactLostWrite(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var mu sync.Mutex
	var writes []records.OrganizationPreviewWrite
	var generation int64
	w := &materialPreviewWriter{life: context.Background(), identity: "library"}
	w.save = func(_ context.Context, r records.OrganizationPreviewWrite) (records.OrganizationPreviewState, error) {
		mu.Lock()
		writes = append(writes, r)
		n := len(writes)
		mu.Unlock()
		if n == 1 {
			close(entered)
			<-release
			generation++
			return records.OrganizationPreviewState{}, errors.New("committed receipt lost")
		}
		if n == 2 {
			if !reflect.DeepEqual(r, writes[0]) {
				t.Error("lost request replaced")
			}
			return records.OrganizationPreviewState{Identity: "library", Generation: generation}, nil
		}
		if r.Generation != generation {
			t.Error("wrong CAS generation", r.Generation, generation)
		}
		generation++
		return records.OrganizationPreviewState{Identity: "library", Generation: generation}, nil
	}
	auto := func(id string, y float32) materialPreviewJob {
		return materialPreviewJob{automatic: true, value: records.OrganizationPreview{ID: id, Scroll: y}}
	}
	w.enqueue(auto("a", 1))
	<-entered
	w.enqueue(auto("a", 2))
	w.enqueue(auto("a", 3))
	w.enqueue(auto("b", 1))
	w.enqueue(auto("b", 2))
	barrier := make(chan error, 1)
	request := records.OrganizationStart{OperationID: "original-start"}
	w.enqueue(materialPreviewJob{value: records.OrganizationPreview{ID: "b", Start: &request}, done: func(err error) { barrier <- err }})
	w.enqueue(auto("a", 4))
	end := make(chan error, 1)
	w.enqueue(materialPreviewJob{value: records.OrganizationPreview{ID: "a", Scroll: 5}, done: func(err error) { end <- err }})
	close(release)
	for _, ch := range []chan error{barrier, end} {
		select {
		case err := <-ch:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("writer failed to drain")
		}
	}
	mu.Lock()
	defer mu.Unlock()
	ids := []string{}
	for _, r := range writes {
		ids = append(ids, r.Preview.ID)
	}
	if !reflect.DeepEqual(ids, []string{"a", "a", "a", "b", "b", "a", "a"}) || writes[2].Preview.Scroll != 3 || writes[3].Preview.Scroll != 2 || writes[4].Preview.Start.OperationID != "original-start" {
		t.Fatal("task coalescing lost draft or barrier", writes)
	}
}

func TestMaterialStartupBrowsingRestoreDoesNotSuppressOpenPreview(t *testing.T) {
	p := records.OrganizationPreview{ID: "preview", Open: true, Kind: "knowledge", Project: "project", TaskID: "task", Candidate: "candidate", Review: true, RejectReasons: map[string]string{"candidate": "尚未提交的理由"}}
	task := records.OrganizationTask{ID: "task", Project: "project", Entries: []records.OrganizationEntry{{ID: "candidate", State: "review_pending", Item: records.OrganizationItem{Action: "merge"}}}}
	a, dispatch := experienceUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
		if r.Op == "organization_preview_get" {
			return serviceipc.Response{OK: true, Result: records.OrganizationPreviewState{Identity: "library", Preview: &p, Task: &task}}
		}
		if r.Op == "organization_preview_put" {
			return serviceipc.Response{OK: true, Result: records.OrganizationPreviewState{Identity: r.OrganizationPreview.Identity, Generation: r.OrganizationPreview.Generation + 1}}
		}
		return serviceipc.Response{OK: false, Error: "unexpected operation"}
	})
	a.guidanceRoot = t.TempDir()
	raw, _ := json.Marshal(BrowsePreferences{Project: "project", Selections: map[string]BrowseSelection{}})
	if err := os.WriteFile(filepath.Join(a.guidanceRoot, "browsing.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	a.materialPersistence = &materialPersistence{}
	a.configureBrowsing()
	a.loadMaterialPreview()
	awaitExperienceDispatch(t, dispatch)
	if a.page != "整理记录" || a.organization.ReviewItem != "candidate" || a.organization.RejectReasons["candidate"].Reason != "尚未提交的理由" {
		t.Fatal("startup project restoration suppressed durable open review", a.page, a.materialPersistence.Touched)
	}
	awaitExperienceDispatch(t, dispatch)
}

func TestMaterialRestoreReadsOriginalTaskAndKeepsNewNavigation(t *testing.T) {
	start := records.OrganizationStart{OperationID: "original", Processing: &records.ProcessingTaskAuthorization{Revision: "original-config", Generation: 1}}
	p := records.OrganizationPreview{ID: "preview", Project: "project", Kind: "knowledge", TaskID: "same-task", Start: &start, Open: true, Review: true, Candidate: "candidate", Focus: "organization-reject-reason-candidate", Scroll: 90, Today: true, OriginFilter: "manual", RejectReasons: map[string]string{"candidate": "尚未提交的拒绝理由"}}
	task := records.OrganizationTask{ID: "same-task", Project: "project", State: "completed", Entries: []records.OrganizationEntry{{ID: "candidate", Item: records.OrganizationItem{Action: "merge"}, State: "review_pending"}}}
	operations := make(chan string, 4)
	a, dispatch := experienceUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
		operations <- r.Op
		if r.Op == "organization_preview_get" {
			return serviceipc.Response{OK: true, Result: records.OrganizationPreviewState{Identity: "library", Preview: &p, Task: &task, Inputs: []records.OrganizationInput{{MergeInput: records.MergeInput{Memory: "source", Revision: 1, Text: "来源v1"}}}}}
		}
		if r.Op == "organization_preview_put" {
			return serviceipc.Response{OK: true, Result: records.OrganizationPreviewState{Identity: r.OrganizationPreview.Identity, Generation: r.OrganizationPreview.Generation + 1}}
		}
		t.Errorf("recovery performed %s", r.Op)
		return serviceipc.Response{OK: false}
	})
	a.materialPersistence = &materialPersistence{}
	a.firstLaunch.Detecting = true
	a.loadMaterialPreview()
	if a.materialPersistence.Loading {
		t.Fatal("read bypassed first detection")
	}
	a.firstLaunch.Detecting = false
	a.loadMaterialPreview()
	a.page = "设置"
	a.materialPersistence.Touched = true
	awaitExperienceDispatch(t, dispatch)
	if a.page != "设置" || a.organization.Selected != task.ID || a.organization.Start != nil || a.organization.RejectReasons["candidate"].Reason != p.RejectReasons["candidate"] || a.organization.Scroll.Y != 90 || a.organization.FilterTime != "今天" {
		t.Fatal("restore changed route/request/draft", a.organization)
	}
	awaitExperienceDispatch(t, dispatch)
	if <-operations != "organization_preview_get" || <-operations != "organization_preview_put" {
		t.Fatal("restore mutated task")
	}
}

func TestMaterialRequestBarrierFailureDoesNotSendReviewAndExitKeepsEditing(t *testing.T) {
	a := New("test", nil)
	a.libraryContext = context.Background()
	dispatch := make(chan func(), 3)
	a.dispatch = func(fn func()) { dispatch <- fn }
	e := records.OrganizationEntry{ID: "entry", State: "review_pending", Item: records.OrganizationItem{Action: "merge"}}
	task := records.OrganizationTask{ID: "task", Project: "project", Entries: []records.OrganizationEntry{e}}
	a.organization.Task = &task
	a.organization.Selected = task.ID
	w := &materialPreviewWriter{life: context.Background(), identity: "library", save: func(context.Context, records.OrganizationPreviewWrite) (records.OrganizationPreviewState, error) {
		return records.OrganizationPreviewState{}, errors.New("transport failed")
	}}
	a.materialPersistence = &materialPersistence{Loaded: true, Writer: w}
	sent := false
	a.organizationAction(func(context.Context) (records.OrganizationTask, error) { sent = true; return task, nil }, false)
	awaitExperienceDispatch(t, dispatch)
	if sent || a.organization.Busy || a.materialPersistence.Error == "" {
		t.Fatal("request bypassed failed durability barrier")
	}
	var quitErr error
	a.FlushDraftPreviews(func(err error) { quitErr = err })
	awaitExperienceDispatch(t, dispatch)
	if quitErr == nil || a.experienceExitPending || a.organization.Task != &task {
		t.Fatal("failed quit discarded editing")
	}
}

func TestMaterialNativeFocusAndTaskDraftIsolation(t *testing.T) {
	a := New("test", nil)
	a.visualFixture = true
	a.page = "整理记录"
	a.organizationMode = true
	source := records.OrganizationInput{MergeInput: records.MergeInput{Memory: "source", Revision: 1, Title: "来源", Text: strings.Repeat("冻结来源中的较长说明。\n", 30)}}
	e := records.OrganizationEntry{ID: "candidate", State: "review_pending", Item: records.OrganizationItem{Action: "overwrite", Target: "source", Title: "候选", Text: "候选正文", Sources: []records.MergeSource{{Memory: "source", Revision: 1}}}}
	task := records.OrganizationTask{ID: "task", Project: "project", State: "completed", Snapshot: records.OrganizationSnapshot{Schema: records.OrganizationReviewSchema, Inputs: []records.OrganizationInput{source}}, Entries: []records.OrganizationEntry{e}}
	p := records.OrganizationPreview{ID: "preview", TaskID: task.ID, Project: task.Project, Kind: "knowledge", Candidate: e.ID, Focus: "organization-reject-reason-" + e.ID, RejectReasons: map[string]string{e.ID: "保留的草稿"}}
	a.adoptMaterialPreview(records.OrganizationPreviewState{Preview: &p, Task: &task, Inputs: task.Snapshot.Inputs})
	a.organization.RejectReasons["foreign"] = &OrganizationRejectDraft{Reason: "其他任务草稿"}
	a.organization.Decisions = map[string]records.OrganizationReview{"foreigntrue": {Item: "foreign"}}
	tt := ui.NewTester(a.View, 980, 620)
	for range 4 {
		tt.Frame()
	}
	if !tt.Focused("拒绝理由（可选）") || a.organization.RestoreFocus || a.organization.Scroll.Y <= 0 {
		t.Fatal("first frames lost focus/visibility", a.organization.Scroll.Y)
	}
	snapshot := a.materialPreviewSnapshot()
	if len(snapshot.RejectReasons) != 1 || len(snapshot.Decisions) != 0 || snapshot.RejectReasons[e.ID] != "保留的草稿" {
		t.Fatal("other task polluted snapshot", snapshot)
	}
	a.organization.RejectReasons[e.ID].Reason = "新的编辑"
	if snapshot.RejectReasons[e.ID] != "保留的草稿" {
		t.Fatal("worker snapshot was mutable")
	}
}

func TestMaterialLibraryRetirementClearsAllDraftsAndPendingRequests(t *testing.T) {
	a := New("test", nil)
	cancelled := false
	a.materialPersistence = &materialPersistence{Cancel: func() { cancelled = true }}
	a.organization.Epoch = "old-library"
	a.organization.Task = &records.OrganizationTask{ID: "old-task"}
	a.organization.Start = &records.OrganizationStart{OperationID: "old-start"}
	a.organization.Resume = &records.OrganizationResume{OperationID: "old-resume"}
	a.organization.Decisions = map[string]records.OrganizationReview{"oldtrue": {Receipt: "old-decision"}}
	a.organization.RejectReasons = map[string]*OrganizationRejectDraft{"old": {Reason: "old-draft"}}
	a.retireMaterialPreview()
	if !cancelled || a.materialPersistence != nil || a.organization.Epoch == "old-library" || a.HasDraftPreview() || len(a.organization.Decisions) != 0 || len(a.organization.RejectReasons) != 0 || a.organization.Resume != nil {
		t.Fatal("old root left usable draft/request", a.organization)
	}
}

func TestMaterialAbandonLostReceiptReplaysOnceAndUnfreezesOnlyAfterProof(t *testing.T) {
	a := New("test", nil)
	a.libraryContext = context.Background()
	dispatch := make(chan func(), 4)
	a.dispatch = func(fn func()) { dispatch <- fn }
	original := &records.OrganizationStart{OperationID: "original-start"}
	a.organization.New = true
	a.organization.Start = original
	a.organization.PreviewID = "same-preview"
	w := &materialPreviewWriter{life: context.Background(), identity: "library"}
	var first *records.OrganizationPreviewWrite
	count := 0
	w.save = func(_ context.Context, r records.OrganizationPreviewWrite) (records.OrganizationPreviewState, error) {
		count++
		if count == 1 {
			first = &r
			return records.OrganizationPreviewState{}, errors.New("committed abandon response lost")
		}
		if count == 2 {
			if !reflect.DeepEqual(r, *first) {
				t.Error("abandon request changed")
			}
			return records.OrganizationPreviewState{Identity: "library", Generation: 1}, nil
		}
		if r.AbandonStart {
			t.Error("abandon submitted twice")
		}
		return records.OrganizationPreviewState{Identity: "library", Generation: r.Generation + 1}, nil
	}
	a.materialPersistence = &materialPersistence{Loaded: true, Writer: w}
	a.abandonMaterialStart()
	awaitExperienceDispatch(t, dispatch)
	if !a.organization.AbandonWanted || a.organization.Start != original {
		t.Fatal("unknown abandon unfroze request")
	}
	a.persistMaterialPreview(nil)
	if count != 1 {
		t.Fatal("automatic save rewrote unknown abandon")
	}
	a.abandonMaterialStart()
	awaitExperienceDispatch(t, dispatch)
	if a.organization.AbandonWanted || a.organization.Start != nil {
		t.Fatal("same abandon receipt not adopted")
	}
	if count != 2 {
		t.Fatal("wrong abandon replay/update count", count)
	}
}
