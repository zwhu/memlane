package desktop

import (
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
)

func TestOrganizationFiltersRetainSelectedTaskAndRetireOldPages(t *testing.T) {
	task := records.OrganizationTask{ID: "selected", State: "completed", Entries: []records.OrganizationEntry{{ID: "item", State: "review_pending", Item: records.OrganizationItem{Action: "merge"}}}}
	queries := make(chan records.OrganizationListQuery, 3)
	a, dispatch := experienceUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
		if r.Op == "organization_list" {
			queries <- *r.OrganizationList
			return serviceipc.Response{OK: true, Result: records.OrganizationPage{Items: []records.OrganizationSummary{}, Total: 0}}
		}
		if r.Op == "organization_get" && r.OrganizationTask == task.ID {
			return serviceipc.Response{OK: true, Result: task}
		}
		t.Errorf("unexpected operation %s", r.Op)
		return serviceipc.Response{OK: false}
	})
	a.visualFixture = false
	a.organization.Selected = task.ID
	a.organization.Task = &task
	a.organization.ReviewItem = "item"
	a.organization.Tasks = make([]records.OrganizationSummary, 40)
	a.organization.Next = "old-cursor"
	a.organization.FilterOrigin = "计划"
	a.changeOrganizationFilters()
	awaitExperienceDispatch(t, dispatch)
	query := <-queries
	if query.Origin != "schedule" || query.Before != "" || a.organization.Next != "" || len(a.organization.Tasks) != 0 || !a.organization.TasksLoaded {
		t.Fatal("filter retained an old page", query, a.organization)
	}
	if a.organization.Selected != task.ID || a.organization.Task.ID != task.ID || a.organization.ReviewItem != "item" {
		t.Fatal("filter changed review context")
	}
	a.page = "整理记录"
	tt := ui.NewTester(a.View, 980, 620)
	if !tt.HasText("没有匹配的整理记录") || !tt.HasText("确认这项改动") {
		t.Fatal("zero results removed selected review")
	}
}

func TestOrganizationRejectRetryKeepsExactReasonAndReceipt(t *testing.T) {
	requests := make(chan records.OrganizationReview, 3)
	a, dispatch := experienceUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
		if r.Op == "organization_review" {
			requests <- *r.OrganizationReview
			return serviceipc.Response{OK: false, Error: "synthetic lost decision response"}
		}
		t.Errorf("unexpected operation %s", r.Op)
		return serviceipc.Response{OK: false}
	})
	a.page = "整理记录"
	e := records.OrganizationEntry{ID: "proposal", State: "review_pending", Item: records.OrganizationItem{Action: "merge", Title: "合成合并"}}
	task := records.OrganizationTask{ID: "task", Entries: []records.OrganizationEntry{e}}
	a.organization.Task = &task
	a.organization.Selected = task.ID
	a.organization.ReviewItem = e.ID
	draft := a.organizationRejectDraft(e.ID)
	draft.Reason = "保留独立依据"
	a.reviewOrganizationEntry(task, e, false)
	awaitExperienceDispatch(t, dispatch)
	first := <-requests
	if first.Confirm || first.RejectionReason != "保留独立依据" || first.Receipt == "" {
		t.Fatal("incorrect rejection request", first)
	}
	draft.Reason = "later edit must not retarget frozen request"
	a.reviewOrganizationEntry(task, e, false)
	awaitExperienceDispatch(t, dispatch)
	again := <-requests
	if first != again {
		t.Fatal("retry replaced original decision", first, again)
	}
	if task.Entries[0].State != "review_pending" || task.Entries[0].Item.Title != "合成合并" {
		t.Fatal("failed rejection changed knowledge/proposal")
	}
	tt := ui.NewTester(a.View, 980, 620)
	if !tt.HasText("重试拒绝回执") {
		t.Fatal("unknown rejection response lost retry")
	}
}

func TestOrganizationReviewNavigationPreservesIndividualReasons(t *testing.T) {
	a, task := materialUIFixture()
	task.Entries[2].State = "review_pending"
	a.organization.Task = &task
	a.openOrganizationEntry("merge")
	a.organization.Scroll.Y = 72
	a.organizationRejectDraft("merge").Reason = "保留来源独立"
	if nextOrganizationReview(task, "merge") != "delete" {
		t.Fatal("next entered automatic row")
	}
	a.openOrganizationEntry("delete")
	a.organization.Scroll.Y = 90
	a.organizationRejectDraft("delete").Reason = "仍需保留原知识"
	a.openOrganizationEntry("merge")
	if a.organization.Scroll.Y != 72 || a.organizationRejectDraft("merge").Reason != "保留来源独立" || a.organizationRejectDraft("delete").Reason != "仍需保留原知识" {
		t.Fatal("next mixed draft or scroll")
	}
	a.showOrganizationResult()
	a.openOrganizationEntry("delete")
	if a.organization.Scroll.Y != 90 || a.organization.Task.ID != task.ID {
		t.Fatal("return lost item context")
	}
	task.Entries = task.Entries[:2]
	if nextOrganizationReview(task, "merge") != "" {
		t.Fatal("single review showed a no-op next item")
	}
}

func TestOrganizationProjectFilterKeepsUniqueNamesAndDisambiguatesDuplicates(t *testing.T) {
	scopes := []records.Scope{{ID: "unique", Kind: "project", Name: "独立项目", Path: "/a"}, {ID: "one", Kind: "project", Name: "同名项目", Path: "/one"}, {ID: "two", Kind: "project", Name: "同名项目", Path: "/two"}, {ID: "global", Kind: "experience", Name: "独立项目"}}
	options, projects := organizationProjectFilterOptions(scopes)
	if len(options) != 4 || projects["独立项目"] != "unique" || projects["同名项目 · /one"] != "one" || projects["同名项目 · /two"] != "two" {
		t.Fatal("project labels are ambiguous or expose unnecessary path", options, projects)
	}
}
