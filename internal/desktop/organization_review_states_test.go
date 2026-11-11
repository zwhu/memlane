package desktop

import (
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
)

func TestMoveReviewConsentCannotTransferBetweenTasksOrProposals(t *testing.T) {
	a, task := materialUIFixture()
	e := task.Entries[1]
	e.Item.Action, e.Item.Destination = "move", "destination"
	task.Snapshot.Destinations = []records.Scope{{ID: "destination", Kind: "project"}}
	a.organization.Task = &task
	a.organization.Task.Entries[1] = e
	a.organization.ReviewItem = e.ID
	a.scopes = []records.Scope{{ID: "project", Kind: "project", Name: "来源"}, {ID: "destination", Kind: "project", Name: "目标"}}
	if a.organizationMoveAuthorized(task, e) {
		t.Fatal("task destination selection was treated as review consent")
	}
	tt := ui.NewTester(a.View, 1200, 900)
	if err := tt.Click("明确授权来源与目标项目的范围变化"); err != nil {
		t.Fatal(err)
	}
	if !a.organizationMoveAuthorized(task, e) {
		t.Fatal("checkbox did not bind the exact task and proposal")
	}
	other := task
	other.ID = "another-task"
	if a.organizationMoveAuthorized(other, e) {
		t.Fatal("consent moved across tasks")
	}
	changed := e
	changed.Item.Destination = "another-destination"
	if a.organizationMoveAuthorized(task, changed) {
		t.Fatal("consent survived a proposal change")
	}
	a.scopes = a.scopes[:1]
	if a.organizationSourcesCurrent(task, e) {
		t.Fatal("removed destination still permitted a new move confirmation")
	}
	a.scopes = append(a.scopes, records.Scope{ID: "destination", Kind: "project", Name: "目标"})
	if err := tt.Click("明确授权来源与目标项目的范围变化"); err != nil {
		t.Fatal(err)
	}
	if a.organizationMoveAuthorized(task, e) {
		t.Fatal("unchecking did not remove consent")
	}
}

func TestReviewSourceFailureStatesDoNotExposeRevokedFrozenBody(t *testing.T) {
	for _, code := range []string{"scope_denied", "connection_revoked", "timeout"} {
		t.Run(code, func(t *testing.T) {
			a, task := materialUIFixture()
			e := task.Entries[1]
			task.Snapshot.Inputs[0].Text = "UNIQUE_PRIVATE_FROZEN_BODY"
			a.organization.Task = &task
			a.organization.ReviewItem = e.ID
			a.organization.SourceErrors = map[string]string{"one": "unreadable"}
			a.organization.SourceErrorCodes = map[string]string{"one": code}
			tt := ui.NewTester(a.View, 980, 700)
			if !tt.HasText("重新整理相关材料") || a.organizationSourcesCurrent(task, e) {
				t.Fatal("failure offered a new confirmation or lost restart entry")
			}
			if code == "timeout" {
				if !tt.HasText("重试核对来源") || a.organizationReviewSourceState(task, e) != "unavailable" {
					t.Fatal("temporary failure was called revocation")
				}
			} else if tt.HasText("UNIQUE_PRIVATE_FROZEN_BODY") || tt.HasText("重试核对来源") || !tt.HasText("当前无权限查看来源原文。") {
				t.Fatal("revoked source body leaked or offered in-place permission retry")
			}
		})
	}
}

func TestReselectChangedReviewUsesCurrentMaterialWithoutStartingTask(t *testing.T) {
	a, task := materialUIFixture()
	e := task.Entries[1]
	a.scopes = []records.Scope{{ID: task.Project, Kind: "project", Name: "原项目"}}
	a.organization.ReviewItem = e.ID
	v := a.organization.Sources["one"]
	v.Revision, v.Text = 2, "current source body"
	a.organization.Sources["one"] = v
	a.organization.SourceErrors = map[string]string{"two": "unreadable"}
	a.organization.SourceErrorCodes = map[string]string{"two": "scope_denied"}
	a.reselectOrganizationMaterials(task, e)
	if !a.organization.New || a.organization.Task != nil || a.organization.Start != nil || a.organization.Project != task.Project || len(a.organization.Chosen) != 1 || a.organization.Chosen["one"].Revision != 2 || a.organization.Chosen["one"].Text != "current source body" {
		t.Fatal("restart used stale material, retained forbidden material, or started a task", a.organization)
	}
	if task.Entries[1].State != "review_pending" {
		t.Fatal("opening selection decided the old suggestion")
	}
}

func TestOnlyAuthoritativeMoveRejectionMayRetireLegacyRequest(t *testing.T) {
	a, task := materialUIFixture()
	e := task.Entries[1]
	e.Item.Action = "move"
	task.Entries[1] = e
	a.organization.Task = &task
	a.organization.ReviewItem = e.ID
	d := records.OrganizationReview{Task: task.ID, Item: e.ID, Digest: records.MergeDigest(e.Item), Confirm: true, Receipt: "original-receipt"}
	a.organization.Decisions = map[string]records.OrganizationReview{e.ID + "true": d}
	if a.retireUnconfirmedLegacyMove(&serviceipc.ServiceError{Code: "service_unavailable"}) || a.organization.Decisions[e.ID+"true"] != d {
		t.Fatal("unknown response discarded original intent")
	}
	if !a.retireUnconfirmedLegacyMove(&serviceipc.ServiceError{Code: "move_scope_confirmation_required"}) || len(a.organization.Decisions) != 0 {
		t.Fatal("authoritative uncommitted legacy request could not seek separate consent")
	}
}

func TestMoveConsentInstructionDoesNotMasqueradeAsFailedSourceRead(t *testing.T) {
	a, task := materialUIFixture()
	e := task.Entries[1]
	e.Item.Action, e.Item.Destination = "move", "destination"
	task.Snapshot.Destinations = []records.Scope{{ID: "destination", Kind: "project"}}
	task.Entries[1] = e
	a.organization.Task = &task
	a.scopes = []records.Scope{{ID: "project", Kind: "project"}, {ID: "destination", Kind: "project"}}
	a.organization.Error = "原移动确认未提交，请先授权范围变化。"
	if !a.organizationSourcesCurrent(task, e) {
		t.Fatal("consent instruction disabled valid consent controls")
	}
	a.organization.SourcesReadFailed = true
	if a.organizationSourcesCurrent(task, e) {
		t.Fatal("failed actual source observation allowed new confirmation")
	}
}

func TestReviewCannotConfirmOrReuseDerivedSourceWithStaleEvidence(t *testing.T) {
	a, task := materialUIFixture()
	e := task.Entries[1]
	v := a.organization.Sources["one"]
	v.Derived = &records.DerivedInfo{State: "stale"}
	a.organization.Sources["one"] = v
	if a.organizationSourcesCurrent(task, e) {
		t.Fatal("stale underlying evidence passed an unchanged revision gate")
	}
	a.scopes = []records.Scope{{ID: task.Project, Kind: "project"}}
	a.organization.ReviewItem = e.ID
	a.reselectOrganizationMaterials(task, e)
	if _, ok := a.organization.Chosen["one"]; ok {
		t.Fatal("reselection reused derived stale evidence")
	}
	if a.organization.Chosen["two"].ID != "two" {
		t.Fatal("unrelated current material was discarded")
	}
}
