package desktop

import (
	"github.com/zwhu/memlane/internal/records"
	"reflect"
	"testing"
)

func TestGraphEntityTypesUseOnlyCurrentExplicitLabels(t *testing.T) {
	a := graphPresentationApp()
	a.browsing.Graph.Project = a.scope
	a.browsing.Graph.Entities = []records.CogneeEntity{{ID: "one", Name: "Type-looking name", Types: []string{"Tool", "Package"}}, {ID: "two", Name: "Package"}}
	if got := a.entityTypeOptions(); !reflect.DeepEqual(got, []string{"不限类型", "Package", "Tool"}) {
		t.Fatal(got)
	}
	if !entityMatchesType(a.browsing.Graph.Entities[0], "Package") || entityMatchesType(a.browsing.Graph.Entities[1], "Package") {
		t.Fatal("type inferred from name or explicit type lost")
	}
	a.browsing.TypeFilter = "Tool"
	a.saveBrowsing()
	if a.browsing.Preferences.Selections[a.scope].TypeFilter != "Tool" {
		t.Fatal("type filter not saved in project context")
	}
	a.scope = "other"
	a.browseProjectChanged()
	if a.browsing.TypeFilter != "" || len(a.entityTypeOptions()) != 1 {
		t.Fatal("type filter crossed projects")
	}
}
