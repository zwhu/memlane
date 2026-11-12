//go:build e2e

package desktop

import (
	"github.com/zwhu/memlane/internal/records"
	"strings"
	"time"
)

// Extends the existing opt-in Native fixture. No service, Agent or model runs.
func (a *App) SetMVPVisualFixture(state string) {
	if strings.HasPrefix(state, "conversation") {
		a.setConversationVisualFixture(state)
		return
	}
	if state == "" {
		return
	}
	const project = "visual-project-a"
	a.scopes = []records.Scope{{ID: project, Kind: "project", Name: "合成界面检查"}}
	a.projects = []ProjectItem{{ID: project, Name: "合成界面检查"}}
	for i := range a.items {
		a.items[i].Dataset = project
		a.items[i].ScopeName = "合成界面检查"
		a.items[i].Origin = "knowledge"
	}
	if len(a.items) < 2 {
		return
	}
	a.items[1].Origin = "raw"
	a.page, a.scope, a.recordKind, a.detailMode = "项目", project, "knowledge", "view"
	a.selectedID, a.selectedItem = a.items[0].ID, &a.items[0]
	if state == "edit" {
		a.editMemory()
		return
	}
	if state == "knowledge" {
		return
	}
	a.page, a.scope, a.organizationMode = "整理记录", "", true
	a.organization = OrganizationUI{
		New: true, Project: project, Kind: "knowledge", TasksLoaded: true,
		Chosen: map[string]MemoryItem{a.items[1].ID: a.items[1]}, Order: []string{a.items[1].ID}, Candidates: a.items,
		ProcessingLoaded: true,
		Processing: records.ProcessingConfiguration{
			Runtime: map[string]records.ProcessingReadiness{"visual": {State: "ready"}},
			Current: records.ProcessingSelection{Active: true, Revision: &records.ProcessingRevision{ID: "visual", Spec: records.ProcessingSpec{Mode: "agent", Agent: "pi", Model: "合成展示"}, Validation: &records.ProcessingValidation{State: "succeeded"}}},
		},
	}
	a.serviceState, a.runtimeStatus.Phase = "ready", "ready"
	// No library context or client is configured: Start cannot dispatch a task.
	// This fixture verifies layout only and cannot write a success receipt.
	if state == "configure" {
		return
	}
	taskState := "completed"
	if state == "running" || state == "failed" || state == "cancelled" {
		taskState = state
	}
	raw, knowledge := a.items[1], a.items[0]
	task := records.OrganizationTask{
		ID: "visual-task", Project: project, State: taskState, Attempt: 1,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		Snapshot: records.OrganizationSnapshot{
			Schema:     records.OrganizationSchema,
			Processing: &records.ProcessingSnapshot{Revision: *a.organization.Processing.Current.Revision},
			Inputs:     []records.OrganizationInput{{MergeInput: records.MergeInput{Memory: raw.ID, Revision: raw.Revision, Title: raw.Title, Text: raw.Text}}},
		},
	}
	if taskState == "completed" {
		task.Result = &records.OrganizationResult{}
		task.Entries = []records.OrganizationEntry{{ID: "visual-summary", State: "applied", Item: records.OrganizationItem{Action: "summary", Title: knowledge.Title, Text: knowledge.Text, Sources: []records.MergeSource{{Memory: raw.ID, Revision: raw.Revision, Quote: raw.Text}}}, Receipt: &records.OrganizationReceipt{Memory: knowledge.ID, Revision: knowledge.Revision}}}
	}
	if taskState == "failed" {
		task.Error = "material_model_failed"
	}
	a.organization.New = false
	a.organization.Selected, a.organization.Task = task.ID, &task
	a.organization.TasksTotal = 1
	spec := a.organization.Processing.Current.Revision.Spec
	a.organization.Tasks = []records.OrganizationSummary{{ID: task.ID, Project: project, State: taskState, Title: "合成界面检查 · 材料整理", Origin: "manual", CreatedAt: task.CreatedAt, Processing: &spec}}
	a.organization.Results = map[string]MemoryItem{knowledge.ID: knowledge}
	a.organization.Sources = map[string]MemoryItem{raw.ID: raw}
	if taskState == "running" {
		a.openOrganizationProgress()
	}
}
