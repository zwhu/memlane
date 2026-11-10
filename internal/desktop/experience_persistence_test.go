package desktop

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
)

func TestExperienceWriterSerializesBarriersCoalescesAndReplaysLostReceipt(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var mu sync.Mutex
	var writes []records.ExperiencePreviewWrite
	var generation int64
	var prior *records.ExperiencePreviewWrite
	w := &experiencePreviewWriter{life: context.Background(), identity: "library"}
	w.save = func(_ context.Context, r records.ExperiencePreviewWrite) (records.ExperiencePreviewState, error) {
		mu.Lock()
		writes = append(writes, r)
		n := len(writes)
		mu.Unlock()
		if n == 1 {
			close(entered)
			<-release
			generation++
			prior = &r
			return records.ExperiencePreviewState{}, errors.New("committed response lost")
		}
		if n == 2 {
			if !reflect.DeepEqual(r, *prior) {
				t.Error("uncertain write was replaced")
			}
			return records.ExperiencePreviewState{Identity: "library", Generation: generation}, nil
		}
		if r.Generation != generation {
			t.Error("concurrent or stale generation", r.Generation, generation)
		}
		generation++
		return records.ExperiencePreviewState{Identity: "library", Generation: generation}, nil
	}
	auto := func(id string) previewJob {
		return previewJob{Automatic: true, value: records.ExperiencePreview{ID: id}}
	}
	w.enqueue(auto("a"))
	<-entered
	w.enqueue(auto("b"))
	w.enqueue(auto("c"))
	done := make(chan error, 1)
	w.enqueue(previewJob{value: records.ExperiencePreview{ID: "request"}, done: func(err error) { done <- err }})
	w.enqueue(auto("old"))
	w.enqueue(auto("new"))
	end := make(chan error, 1)
	w.enqueue(previewJob{value: records.ExperiencePreview{ID: "quit"}, done: func(err error) { end <- err }})
	close(release)
	for _, ch := range []chan error{done, end} {
		select {
		case err := <-ch:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("writer did not drain")
		}
	}
	mu.Lock()
	defer mu.Unlock()
	ids := []string{}
	for _, r := range writes {
		ids = append(ids, r.Preview.ID)
	}
	if !reflect.DeepEqual(ids, []string{"a", "a", "c", "request", "new", "quit"}) {
		t.Fatal("barrier/coalescing order", ids)
	}
}

func TestExperienceWriterDoesNotAdoptForeignLibraryReceipt(t *testing.T) {
	value := records.ExperiencePreview{ID: "retained-local-draft"}
	w := &experiencePreviewWriter{life: context.Background(), identity: "old-library", save: func(_ context.Context, r records.ExperiencePreviewWrite) (records.ExperiencePreviewState, error) {
		return records.ExperiencePreviewState{Identity: "new-library", Generation: r.Generation + 1}, nil
	}}
	done := make(chan error, 1)
	w.enqueue(previewJob{value: value, done: func(err error) { done <- err }})
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("foreign receipt accepted")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("writer callback absent")
	}
	if w.identity != "old-library" || w.generation != 0 {
		t.Fatal("writer switched libraries")
	}
}

func TestExperienceWriterAllowsCorrectionAfterExplicitValidationRejection(t *testing.T) {
	w := &experiencePreviewWriter{life: context.Background(), identity: "library"}
	w.save = func(_ context.Context, r records.ExperiencePreviewWrite) (records.ExperiencePreviewState, error) {
		if r.Preview.ID == "invalid" {
			return records.ExperiencePreviewState{}, &serviceipc.ServiceError{Code: "invalid_preview", Message: "size rejected"}
		}
		return records.ExperiencePreviewState{Identity: r.Identity, Generation: r.Generation + 1}, nil
	}
	done := make(chan error, 2)
	w.enqueue(previewJob{value: records.ExperiencePreview{ID: "invalid"}, done: func(err error) { done <- err }})
	if err := <-done; err == nil {
		t.Fatal("invalid preview accepted")
	}
	w.enqueue(previewJob{value: records.ExperiencePreview{ID: "corrected"}, done: func(err error) { done <- err }})
	select {
	case err := <-done:
		if err != nil {
			t.Fatal("rejected draft poisoned subsequent correction", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("correction callback absent")
	}
}

func TestExperienceRestoreWaitsForDetectionAndKeepsNewNavigation(t *testing.T) {
	input := records.MergeInput{Memory: "source", Revision: 1, Title: "冻结来源", Text: "来源v1"}
	p := records.ExperiencePreview{ID: "preview", Project: "project", TaskID: "same-task", Source: records.MergeTarget{Memory: "source", Revision: 1}, Open: true, Page: "通用知识", ReturnPage: "项目", Drafts: map[string]records.ExperiencePreviewDraft{"candidate": {Value: records.ExperienceReviewEdit{Text: "用户草稿"}}}}
	a, dispatch := experienceUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
		if r.Op == "experience_preview_get" {
			return serviceipc.Response{OK: true, Result: records.ExperiencePreviewState{Identity: "library", Preview: &p, Source: &input, Task: &records.OrganizationTask{ID: "same-task"}}}
		}
		if r.Op == "experience_preview_put" {
			return serviceipc.Response{OK: true, Result: records.ExperiencePreviewState{Identity: r.ExperiencePreview.Identity, Generation: r.ExperiencePreview.Generation + 1}}
		}
		return serviceipc.Response{OK: false, Error: "unexpected operation"}
	})
	a.experiencePersistence = &experiencePersistence{}
	a.firstLaunch.Detecting = true
	a.loadExperiencePreview()
	if a.experiencePersistence.Loading {
		t.Fatal("preview bypassed first detection")
	}
	a.firstLaunch.Detecting = false
	a.loadExperiencePreview()
	a.page = "设置"
	a.experiencePersistence.Touched = true
	awaitExperienceDispatch(t, dispatch)
	if a.page != "设置" || a.experience.Distill == nil || a.experience.Distill.Open || a.experience.Distill.TaskID != "same-task" || a.organization.ExperienceDrafts["candidate"].Value.Text != "用户草稿" {
		t.Fatal("recovery replaced newer navigation or lost retained preview")
	}
	awaitExperienceDispatch(t, dispatch)
}

func TestExperienceFirstNativeFramesRestoreActualFocusAndVisibility(t *testing.T) {
	a := New("test", nil)
	item := records.OrganizationItem{Action: "global_experience", Title: "候选标题", Text: "用户待确认的正文", Conditions: "项目约定优先"}
	entry := records.OrganizationEntry{ID: "candidate", State: "review_pending", Item: item}
	source := MemoryItem{ID: "source", Dataset: "project", Revision: 1, State: "active", Text: strings.Repeat("冻结来源中的较长说明。\n", 30)}
	s := &ExperienceDistillUI{Open: true, Source: source, CurrentSource: source, TaskID: "task", Task: &records.OrganizationTask{ID: "task", State: "completed", Entries: []records.OrganizationEntry{entry}}, CandidateID: entry.ID, Focus: "experience-review-conditions-candidate", RestoreFocus: true}
	a.page = "通用知识"
	a.experience.Distill = s
	tt := ui.NewTester(a.View, 980, 620)
	for range 4 {
		tt.Frame()
	}
	if !tt.Focused("适用条件") || s.RestoreFocus || a.detailScroll.Y <= 0 {
		t.Fatal("born field lost focus or remained outside viewport", s.RestoreFocus, a.detailScroll.Y)
	}
}
