package desktop

import (
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
)

func deletionImpactUIFixture(t *testing.T, response serviceipc.Response) (*App, chan func(), records.OrganizationEntry) {
	t.Helper()
	a, dispatch := experienceUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
		if r.Op != "deletion_impact" || r.Memory != "target" || r.Scope != "project" || r.ExpectedRevision != 3 || len(r.Datasets) != 1 || r.Datasets[0] != "project" {
			t.Errorf("unexpected request %#v", r)
		}
		return response
	})
	a.visualFixture = false
	e := records.OrganizationEntry{ID: "delete", State: "review_pending", Item: records.OrganizationItem{Action: "delete", Target: "target", Sources: []records.MergeSource{{Memory: "target", Revision: 3}}}}
	task := records.OrganizationTask{ID: "task", Project: "project", Entries: []records.OrganizationEntry{e}, Snapshot: records.OrganizationSnapshot{Inputs: []records.OrganizationInput{{MergeInput: records.MergeInput{Memory: "target", Revision: 3}}}}}
	a.scopes = []records.Scope{{ID: "project", Kind: "project", Name: "项目"}}
	a.page = "整理记录"
	a.organization.Task, a.organization.Selected, a.organization.ReviewItem = &task, task.ID, e.ID
	a.organization.Sources = map[string]MemoryItem{"target": {ID: "target", Dataset: "project", State: "active", Revision: 3}}
	return a, dispatch, e
}

func TestDeletionImpactReadFailureIsUnknownAndCannotConfirm(t *testing.T) {
	a, dispatch, e := deletionImpactUIFixture(t, serviceipc.Response{OK: false, Code: "service_unavailable", Error: "unavailable"})
	a.refreshOrganizationDeletionImpact()
	awaitExperienceDispatch(t, dispatch)
	tt := ui.NewTester(a.View, 980, 700)
	if a.organizationDeletionImpactCurrent(*a.organization.Task, e) || !tt.HasText("引用影响尚未核对成功，数量未知。请核对来源版本、服务与范围后重试。") || tt.HasText("本次核对：0 条当前知识 · 0 处直接来源引用") {
		t.Fatal("failure was presented as an empty impact")
	}
	a.reviewOrganizationEntry(*a.organization.Task, e, true)
	if len(a.organization.Decisions) != 0 || a.organization.Busy {
		t.Fatal("unknown impact created or sent a deletion request")
	}
}

func TestDeletionImpactKnownZeroMatchesExactTargetAndRetiresAcrossEpoch(t *testing.T) {
	r := records.DeletionImpact{Memory: "target", Dataset: "project", Revision: 3, CoverageDatasets: []string{"project"}, References: []records.DeletionReference{}}
	a, dispatch, e := deletionImpactUIFixture(t, serviceipc.Response{OK: true, Result: r})
	a.refreshOrganizationDeletionImpact()
	awaitExperienceDispatch(t, dispatch)
	if !a.organizationDeletionImpactCurrent(*a.organization.Task, e) {
		t.Fatal("known exact zero was not usable")
	}
	tt := ui.NewTester(a.View, 980, 700)
	if !tt.HasText("本次核对：0 条当前知识 · 0 处直接来源引用") {
		t.Fatal("known zero was not distinct from unknown")
	}
	a.libraryEpoch++
	if a.organizationDeletionImpactCurrent(*a.organization.Task, e) {
		t.Fatal("another library reused previous impact")
	}
	a.refreshOrganizationDeletionImpact()
	a.libraryEpoch++
	awaitExperienceDispatch(t, dispatch)
	if a.organization.DeletionImpact.Result != nil {
		t.Fatal("late old-library receipt entered the current preview")
	}
}
