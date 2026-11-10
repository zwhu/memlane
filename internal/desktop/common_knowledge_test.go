package desktop

import (
	"reflect"
	"testing"

	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
)

func TestCommonCategoryKeepsMatchingSelectionAndNeverReadsProject(t *testing.T) {
	scopes := []records.Scope{{ID: "p", Kind: "preference", Name: "个人偏好"}, {ID: "e", Kind: "experience", Name: "全局经验"}, {ID: "project", Kind: "project", Name: "项目"}}
	a, dispatched := experienceUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
		switch r.Op {
		case "scopes":
			return serviceipc.Response{OK: true, Result: scopes}
		case "list":
			for _, id := range r.ListQuery.Datasets {
				if id == "project" {
					return serviceipc.Response{OK: false, Error: "project must never enter common query"}
				}
			}
			v := records.Version{Memory: "selected", Dataset: "e", ScopeKind: "experience", ScopeName: "全局经验", Origin: "knowledge", Revision: 1}
			n := 1
			return serviceipc.Response{OK: true, Result: records.Page{Items: []records.Version{v}, Total: 1, KnowledgeTotal: &n}}
		case "get":
			return serviceipc.Response{OK: true, Result: records.Version{Memory: r.Memory, Dataset: "e", ScopeKind: "experience", ScopeName: "全局经验", Origin: "knowledge", Revision: 1}}
		case "history":
			return serviceipc.Response{OK: true, Result: records.HistoryPage{}}
		case "activities":
			return serviceipc.Response{OK: true, Result: records.ActivitySummary{}}
		case "merge_list":
			return serviceipc.Response{OK: true, Result: records.MergePage{}}
		case "organization_sources":
			return serviceipc.Response{OK: true, Result: []records.OrganizationSource{}}
		}
		return serviceipc.Response{OK: false, Error: r.Op}
	})
	a.visualFixture = false
	a.page, a.scope = "通用知识", ""
	a.scopes = scopes
	v := MemoryItem{ID: "selected", Dataset: "e", Origin: "knowledge", Revision: 1}
	a.selectedID, a.selectedItem = v.ID, &v
	a.selectCommonCategory("experience")
	if a.selectedID != v.ID || a.selectedItem != &v {
		t.Fatal("matching detail discarded")
	}
	awaitExperienceDispatch(t, dispatched)
	if a.libraryError != "" || a.selectedID != v.ID || a.selectedItem.ScopeName != "通用经验" {
		t.Fatal(a.libraryError, a.selectedItem)
	}
	a.selectCommonCategory("experience")
	awaitExperienceDispatch(t, dispatched)
	if a.commonCategory != "" || a.selectedID != v.ID {
		t.Fatal("clearing category lost matching detail")
	}
}

func TestCommonCategoryMissingScopeFailsClosed(t *testing.T) {
	a, dispatched := experienceUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
		if r.Op == "scopes" {
			return serviceipc.Response{OK: true, Result: []records.Scope{{ID: "project", Kind: "project", Name: "项目"}}}
		}
		return serviceipc.Response{OK: false, Error: "unexpected read: " + r.Op}
	})
	a.visualFixture = false
	a.page, a.scope = "通用知识", ""
	a.selectedID, a.selectedItem = "", nil
	a.selectCommonCategory("preference")
	awaitExperienceDispatch(t, dispatched)
	if a.libraryError == "" || a.selectedID != "" || len(a.items) != 0 {
		t.Fatal("missing common scopes expanded query")
	}
}

func TestCommonDatasetsDisplayAndScopeIdentity(t *testing.T) {
	a := New("test", nil)
	a.scopes = []records.Scope{{ID: "p", Kind: "preference", Name: "个人偏好"}, {ID: "e", Kind: "experience", Name: "全局经验"}, {ID: "x", Kind: "project", Name: "通用经验"}}
	got, err := commonDatasets(a.scopes, "")
	if err != nil || !reflect.DeepEqual(got, []string{"p", "e"}) {
		t.Fatal(got, err)
	}
	if a.datasetForLabel("全局经验") != "e" || a.scopeLabel("e") != "通用经验 · e" || a.datasetForLabel("通用经验 · e") != "e" || a.datasetForLabel("通用经验 · x") != "x" {
		t.Fatal("display names changed identity")
	}
	if _, err = commonDatasets(a.scopes, "bogus"); err == nil {
		t.Fatal("unknown category expanded query")
	}
	v := displayVersion(records.Version{ScopeKind: "experience", ScopeName: "全局经验", Source: "用户：原文复制到全局"})
	if v.ScopeName != "通用经验" || v.Source != "用户：原文复制到通用知识" {
		t.Fatal(v)
	}
}

func TestCommonRefreshDoesNotReplaceNewEditor(t *testing.T) {
	scopes := []records.Scope{{ID: "e", Kind: "experience", Name: "全局经验"}, {ID: "p", Kind: "preference", Name: "个人偏好"}}
	a, dispatched := experienceUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
		v := records.Version{Memory: "e-item", Dataset: "e", ScopeKind: "experience", Origin: "knowledge", State: "active", Revision: 1}
		switch r.Op {
		case "scopes":
			return serviceipc.Response{OK: true, Result: scopes}
		case "list":
			return serviceipc.Response{OK: true, Result: records.Page{Items: []records.Version{v}, Total: 1}}
		case "get":
			return serviceipc.Response{OK: true, Result: v}
		case "activities":
			return serviceipc.Response{OK: true, Result: records.ActivitySummary{}}
		case "history":
			return serviceipc.Response{OK: true, Result: records.HistoryPage{}}
		case "merge_list":
			return serviceipc.Response{OK: true, Result: records.MergePage{}}
		case "organization_sources":
			return serviceipc.Response{OK: true, Result: []records.OrganizationSource{}}
		}
		return serviceipc.Response{OK: false, Error: r.Op}
	})
	a.visualFixture = false
	a.page, a.scope = "通用知识", ""
	a.scopes = scopes
	v := MemoryItem{ID: "e-item", Dataset: "e", Origin: "knowledge", State: "active", Revision: 1}
	a.selectedID, a.selectedItem = v.ID, &v
	a.refresh(false)
	fn := <-dispatched
	a.newMemory()
	a.draft = "未保存的个人偏好草稿"
	fn()
	if a.selectedID != v.ID || !a.editing || a.detailMode != "new" || a.draft != "未保存的个人偏好草稿" {
		t.Fatal("late common refresh replaced new editor")
	}
}
