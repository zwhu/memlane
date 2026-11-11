package desktop

import (
	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/records"
	"testing"
)

func TestUnrelatedKnowledgeDoesNotReturnToRememberedTask(t *testing.T) {
	a := sourceReadingFrame()
	a.closeSourceReader()
	a.organization.Selected = "old-summary"
	a.organization.Task = &records.OrganizationTask{ID: "old-summary", Entries: []records.OrganizationEntry{{Receipt: &records.OrganizationReceipt{Memory: "other-summary"}}}}
	a.merges.Selected = "old-merge"
	a.merges.Task = &records.MergeTask{ID: "old-merge", Receipt: &records.MergeReceipt{Memory: "other-merged"}}
	tt := ui.NewTester(a.View, 1200, 820)
	if tt.HasText("返回摘要任务") || tt.HasText("返回整理任务") {
		t.Fatal("unrelated task appeared as reading context")
	}
	a.organization.Task.Entries[0].Receipt.Memory = a.selectedID
	tt.SetSize(1200, 820)
	if !tt.HasText("返回摘要任务") || tt.HasText("返回整理任务") {
		t.Fatal("actual result lost its task return")
	}
}
