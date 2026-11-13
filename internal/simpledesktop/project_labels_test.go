package simpledesktop

import "testing"

func TestSameNameProjectsHaveDistinctChoicesAndNeverShareSearchScope(t *testing.T) {
	projects := []Project{{ID: "one", Name: "same", Path: "/work/one/same", Dataset: "data-one"}, {ID: "two", Name: "same", Path: "/work/two/same", Dataset: "data-two"}, {ID: "all", Name: "全部项目", Path: "/work/all", Dataset: "data-all"}}
	labels := projectLabels(projects)
	if labels[0] != "same · one" || labels[1] != "same · two" || labels[2] == "全部项目" {
		t.Fatalf("ambiguous choices: %v", labels)
	}
	rows := searchProjects(projects, "two")
	if len(rows) != 1 || rows[0].Dataset != "data-two" {
		t.Fatalf("same names combined search scopes: %v", rows)
	}
	if len(searchProjects(projects, "")) != 3 || len(searchProjects(projects, "missing")) != 0 {
		t.Fatal("unknown identity fell back to another project")
	}
}
