package desktop

import (
	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/records"
	"testing"
)

func previewLabelState() records.OrganizationPreviewState {
	return records.OrganizationPreviewState{Preview: &records.OrganizationPreview{ID: "preview", Project: "593fbc66-5917-4dd7-99c3-861b355be913", Kind: "knowledge", New: true, Review: true}, Inputs: []records.OrganizationInput{{MergeInput: records.MergeInput{Memory: "manual", Revision: 5, Title: "manual", Text: "synthetic"}}, {MergeInput: records.MergeInput{Memory: "duplicate", Revision: 1, Title: "duplicate", Text: "synthetic"}}}}
}
func TestMaterialPreviewProjectLabelFollowsLateScopesAndSameProjectKeepsMaterials(t *testing.T) {
	a := New("test", nil)
	a.visualFixture = true
	state := previewLabelState()
	a.adoptMaterialPreview(state)
	if a.organization.Project != state.Preview.Project || len(a.organization.Chosen) != 2 {
		t.Fatal("preview not restored")
	}
	a.scopes = []records.Scope{{ID: state.Preview.Project, Kind: "project", Name: "第九包连续验收项目"}, {ID: "other", Kind: "project", Name: "其他项目"}}
	tt := ui.NewTester(func(c *ui.Context) { a.organizationChooser(c) }, 1200, 1100)
	if a.organization.ProjectLabel != "第九包连续验收项目" {
		t.Errorf("late scopes left restored project selector blank: %q", a.organization.ProjectLabel)
	}
	if e := tt.Click("整理项目"); e != nil {
		t.Fatal(e)
	}
	if e := tt.Click("第九包连续验收项目"); e != nil {
		t.Fatal(e)
	}
	if a.organization.Project != state.Preview.Project || len(a.organization.Chosen) != 2 || len(a.organization.Order) != 2 || a.organization.Order[0] != "manual" || a.organization.Order[1] != "duplicate" {
		t.Fatal("same project reselection discarded restored material draft")
	}
}
func TestMaterialPreviewUnknownProjectNeverMapsToAnotherScope(t *testing.T) {
	a := New("test", nil)
	a.visualFixture = true
	state := previewLabelState()
	state.Preview.Start = &records.OrganizationStart{OperationID: "unknown-start", Project: state.Preview.Project}
	a.adoptMaterialPreview(state)
	a.scopes = []records.Scope{{ID: "other", Kind: "project", Name: "第九包连续验收项目"}}
	ui.NewTester(func(c *ui.Context) { a.organizationChooser(c) }, 1200, 1100)
	if a.organization.Project != state.Preview.Project || a.organization.ProjectLabel != "" || a.organization.Start != state.Preview.Start || len(a.organization.Chosen) != 2 {
		t.Fatal("unknown project identity or original start replaced")
	}
}

func TestMaterialPreviewExactProjectRenameAndExplicitDifferentSelection(t *testing.T) {
	a := New("test", nil)
	a.visualFixture = true
	state := previewLabelState()
	a.scopes = []records.Scope{{ID: state.Preview.Project, Kind: "project", Name: "同名项目"}, {ID: "different-project", Kind: "project", Name: "同名项目"}}
	a.adoptMaterialPreview(state)
	a.organization.ReviewMode = false // Avoid matching the separate destination checkbox when exercising Select.
	tt := ui.NewTester(func(c *ui.Context) { a.organizationChooser(c) }, 1200, 1100)
	if a.organization.ProjectLabel != a.scopeLabel(state.Preview.Project) || a.organization.ProjectLabel == a.scopeLabel("different-project") {
		t.Fatal("same names lost exact project identity")
	}
	a.scopes[0].Name = "重命名项目"
	tt.Frame()
	if a.organization.ProjectLabel != "重命名项目" || len(a.organization.Chosen) != 2 {
		t.Fatal("rename changed material ownership")
	}
	if e := tt.Click("整理项目"); e != nil {
		t.Fatal(e)
	}
	if e := tt.Click("同名项目"); e != nil {
		t.Fatal(e)
	}
	if a.organization.Project != "different-project" || len(a.organization.Chosen) != 0 || len(a.organization.Order) != 0 {
		t.Fatalf("explicit different project did not use original cleanup project=%q label=%q chosen=%d order=%v", a.organization.Project, a.organization.ProjectLabel, len(a.organization.Chosen), a.organization.Order)
	}
}
