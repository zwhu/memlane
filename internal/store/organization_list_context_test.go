package store

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

// Real SQLite/query/plan transactions; all model results are explicit fixtures.
func TestOrganizationHistoryFiltersTotalAndPagination(t *testing.T) {
	s, d, now := scheduleFixture(t)
	d.Enabled = true
	plan, err := s.PutSchedule(mergeCtx, d, now)
	mustMerge(t, err)
	_, scheduled, err := s.FireSchedule(mergeCtx, plan.ID, plan.Generation, time.UnixMilli(plan.NextRunMS).Add(time.Second))
	mustMerge(t, err)
	if scheduled == nil {
		t.Fatal("no formal scheduled task")
	}
	completeEmptyScheduleFixture(t, s, *scheduled)
	_, err = s.DB.Exec(`UPDATE organization_tasks SET created_at='2026-01-01 10:00:01' WHERE id=?`, scheduled.ID)
	mustMerge(t, err)
	d.Enabled, d.ExpectedGeneration, d.OperationID = false, plan.Generation, uuid.NewString()
	_, err = s.PutSchedule(mergeCtx, d, now)
	mustMerge(t, err)
	for i := 0; i < 35; i++ {
		start := records.OrganizationStart{OperationID: uuid.NewString(), Project: scheduled.Project}
		for _, input := range scheduled.Snapshot.Inputs {
			start.Inputs = append(start.Inputs, records.MergeTarget{Memory: input.Memory, Revision: input.Revision})
		}
		task, _, err := s.StartOrganization(mergeCtx, start)
		mustMerge(t, err)
		created := now.Add(time.Duration(i) * time.Minute)
		if i < 5 {
			created = created.AddDate(0, 0, -1)
		}
		_, err = s.DB.Exec(`UPDATE organization_tasks SET created_at=? WHERE id=?`, created.UTC().Format("2006-01-02 15:04:05"), task.ID)
		mustMerge(t, err)
	}
	q := records.OrganizationListQuery{MergeListQuery: records.MergeListQuery{Project: scheduled.Project, Limit: 10}, Origin: "manual"}
	seen := map[string]bool{}
	for {
		page, err := s.ListOrganizations(mergeCtx, q)
		mustMerge(t, err)
		if page.Total != 35 {
			t.Fatal("total changed with cursor", page)
		}
		for _, item := range page.Items {
			if seen[item.ID] || item.Origin != "manual" || item.Project != scheduled.Project {
				t.Fatal("filter or cursor mixed task", item)
			}
			seen[item.ID] = true
		}
		if page.Next == "" {
			break
		}
		q.Before = page.Next
	}
	if len(seen) != 35 {
		t.Fatal("pagination missed tasks", len(seen))
	}
	q.Before, q.Origin = "", "schedule"
	page, err := s.ListOrganizations(mergeCtx, q)
	mustMerge(t, err)
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].ID != scheduled.ID || page.Items[0].Origin != "schedule" {
		t.Fatal("disabled plan lost historical origin", page)
	}
	q.Origin = "manual"
	q.Before = scheduled.ID
	if _, err = s.ListOrganizations(mergeCtx, q); err == nil {
		t.Fatal("cursor from other filter accepted")
	}
	q.Before = ""
	q.SinceMS = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	page, err = s.ListOrganizations(mergeCtx, q)
	mustMerge(t, err)
	if page.Total != 30 {
		t.Fatal("time filter total incorrect", page.Total)
	}
	q.SinceMS = now.AddDate(1, 0, 0).UnixMilli()
	page, err = s.ListOrganizations(mergeCtx, q)
	mustMerge(t, err)
	if page.Total != 0 || len(page.Items) != 0 || page.Next != "" {
		t.Fatal("zero match returned history", page)
	}
}

func TestOrganizationListShowsFrozenProcessing(t *testing.T) {
	s, _ := mergeDB(t)
	base, _, _ := organizationFixture(t, s)
	first, err := s.SaveProcessing(mergeCtx, records.ProcessingSave{OperationID: uuid.NewString(), Spec: processingProvider("history-first")}, processingTime)
	mustMerge(t, err)
	dependency := records.MergeDigest("history-first-dependency")
	processingVerifiedFixture(t, s, first.ID, dependency)
	_, err = s.ActivateProcessing(mergeCtx, records.ProcessingActivation{OperationID: uuid.NewString(), Revision: first.ID}, dependency, processingTime)
	mustMerge(t, err)
	d := records.OrganizationStart{OperationID: uuid.NewString(), Project: base.Project, Processing: &records.ProcessingTaskAuthorization{Revision: first.ID, Generation: 1}}
	for _, input := range base.Snapshot.Inputs {
		d.Inputs = append(d.Inputs, records.MergeTarget{Memory: input.Memory, Revision: input.Revision})
	}
	task, _, err := s.StartProcessingOrganization(mergeCtx, d, dependency)
	mustMerge(t, err)
	_, err = s.SaveProcessing(mergeCtx, records.ProcessingSave{OperationID: uuid.NewString(), ExpectedSaved: first.ID, Spec: processingProvider("history-second")}, processingTime)
	mustMerge(t, err)
	page, err := s.ListOrganizations(mergeCtx, records.OrganizationListQuery{})
	mustMerge(t, err)
	found := false
	for _, item := range page.Items {
		if item.ID == task.ID {
			found = true
			if item.Processing == nil || item.Processing.Model != "history-first" {
				t.Fatal("list adopted newer settings", item)
			}
		}
	}
	if !found {
		t.Fatal("task missing from history")
	}
}
