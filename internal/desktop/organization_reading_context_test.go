package desktop

import "testing"

func TestOrganizationReadingUsesResultTypeAndPreservesOtherDraft(t *testing.T) {
	a := New("test", nil)
	a.visualFixture = true
	a.page = "整理记录"
	a.routeStates["项目:project"] = PaneState{ID: "prior", Mode: "edit", Kind: "raw"}
	a.drafts["prior"] = Draft{Text: "unsaved original draft", Scope: "project"}
	a.openMergeMemory("summary", "project")
	if a.recordKind != "knowledge" || a.listQuery().Kind != "knowledge" || a.selectedID != "summary" || a.editing || a.detailMode != "view" {
		t.Fatal("summary opened inside a restored raw/editor context")
	}
	if a.drafts["prior"].Text != "unsaved original draft" {
		t.Fatal("opening a result discarded another record's draft")
	}
	a.navigate("整理记录", "")
	a.openMergeMemory("raw-result", "project", "raw")
	if a.recordKind != "raw" || a.listQuery().Kind != "raw" || a.selectedID != "raw-result" {
		t.Fatal("raw result was relabelled as knowledge")
	}
}
