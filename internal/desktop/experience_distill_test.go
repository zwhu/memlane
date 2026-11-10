package desktop

// Native state fixtures exercise immutable drafts and navigation. They do not
// represent model generation; the packaged pi run is verified separately.
import (
	"context"
	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
	"testing"
)

func TestDistillNativeDraftPreservesOriginalAndRejectsSourceChange(t *testing.T) {
	a, _ := experienceUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
		t.Errorf("invalid preview sent IPC %s", r.Op)
		return serviceipc.Response{OK: false}
	})
	source := *a.current()
	item := records.OrganizationItem{Action: "global_experience", Title: "模型候选标题", Text: "模型候选正文", Conditions: "模型候选适用条件", Destination: "global"}
	entry := records.OrganizationEntry{ID: "candidate", Item: item, State: "review_pending"}
	task := records.OrganizationTask{ID: "task", State: "completed", Entries: []records.OrganizationEntry{entry}}
	s := &ExperienceDistillUI{Open: true, Source: source, CurrentSource: source, TaskID: task.ID, Task: &task, CandidateID: entry.ID, ReturnPage: "项目", ReturnScope: source.Dataset, ReturnScroll: 100}
	a.experience.Distill = s
	a.page, a.scope = "通用知识", ""
	draft := a.experienceReviewDraft(entry)
	draft.Value.Title = "用户确认标题"
	draft.Value.Text = "用户编辑的通用表述"
	draft.Value.Conditions = "用户确认适用条件"
	tt := ui.NewTester(a.View, 1280, 900)
	if !tt.HasText("先看清通用表述与适用条件") || !a.commonDistillOpen() {
		t.Fatal("accepted preview missing")
	}
	if task.Entries[0].Item.Title != item.Title {
		t.Fatal("draft changed model proposal")
	}
	s.CurrentSource.Revision++
	a.confirmDistillExperience(entry, true)
	if s.Busy || draft.Request != nil {
		t.Fatal("stale source confirmation sent")
	}
	s.Task.Entries[0].State = "expired"
	staleView := ui.NewTester(a.View, 1280, 900)
	if _, ok := staleView.Find("确认保存为通用经验"); !ok {
		t.Fatal("expired source removed the blocked confirmation instead of preserving its preview")
	}
	a.closeExperienceDistill()
	if a.page != "项目" || a.scope != source.Dataset || a.detailScroll.Y != 100 || s.Open {
		t.Fatal("closing changed source reading")
	}
	a.showExperienceDistill()
	if !a.commonDistillOpen() || a.experienceReviewDraft(entry).Value.Text != "用户编辑的通用表述" {
		t.Fatal("reentry lost draft or started another task")
	}
}

func TestDistillDraftOriginalCollapsibleHasStableState(t *testing.T) {
	a := New("test", nil)
	e := records.OrganizationEntry{ID: "one", Item: records.OrganizationItem{Action: "global_experience", Title: "original", Text: "body", Conditions: "limits"}}
	d := a.experienceReviewDraft(e)
	d.OriginalOpen = true
	if a.experienceReviewDraft(e) != d || !a.experienceReviewDraft(e).OriginalOpen {
		t.Fatal("rerender lost original candidate visibility")
	}
	e.Item.Text = "another model proposal"
	changed := a.experienceReviewDraft(e)
	if changed == d || changed.Value.Text != e.Item.Text {
		t.Fatal("new proposal reused old edited draft")
	}
}

func TestDistillCommittedReceiptRetrySurvivesSourceChange(t *testing.T) {
	var requests []records.OrganizationReview
	a, dispatch := experienceUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
		if r.Op != "organization_review" {
			return serviceipc.Response{OK: false, Error: "unexpected fixture operation"}
		}
		requests = append(requests, *r.OrganizationReview)
		if len(requests) == 1 {
			return serviceipc.Response{OK: false, Code: "service_error", Error: "reply lost after a committed approval"}
		}
		return serviceipc.Response{OK: true, Result: records.OrganizationTask{ID: "task", State: "completed", Entries: []records.OrganizationEntry{{ID: "candidate", State: "applied", Receipt: &records.OrganizationReceipt{Memory: "saved", Revision: 1}}}}}
	})
	source := *a.current()
	e := records.OrganizationEntry{ID: "candidate", State: "review_pending", Item: records.OrganizationItem{Action: "global_experience", Title: "标题", Text: "通用表述", Conditions: "适用条件"}}
	a.experiencePersistence = &experiencePersistence{Loaded: true, Writer: &experiencePreviewWriter{life: context.Background(), identity: "fixture-identity", save: func(_ context.Context, w records.ExperiencePreviewWrite) (records.ExperiencePreviewState, error) {
		return records.ExperiencePreviewState{Identity: w.Identity, Generation: w.Generation + 1}, nil
	}}}
	s := &ExperienceDistillUI{Open: true, Source: source, CurrentSource: source, TaskID: "task", Task: &records.OrganizationTask{ID: "task", State: "completed", Entries: []records.OrganizationEntry{e}}}
	a.experience.Distill = s
	a.confirmDistillExperience(e, true)
	awaitExperienceDispatch(t, dispatch)
	awaitExperienceDispatch(t, dispatch)
	if s.Error == "" || a.experienceReviewDraft(e).Request == nil {
		t.Fatal("uncertain confirmation lost its immutable request")
	}
	s.CurrentSource.Revision++
	a.confirmDistillExperience(e, true)
	awaitExperienceDispatch(t, dispatch)
	awaitExperienceDispatch(t, dispatch)
	if len(requests) != 2 || requests[0].Receipt != requests[1].Receipt || requests[1].Edit.Text != "通用表述" || s.Task.Entries[0].State != "applied" {
		t.Fatal("source change prevented receipt recovery or changed the request")
	}
}
