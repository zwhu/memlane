package simpledesktop

import "testing"

func TestProjectDirectoryCatalog(t *testing.T) {
	t.Run("remove unrelated project preserves selected identity and reading", func(t *testing.T) {
		a := New(Config{ServiceURL: "http://127.0.0.1:18999"}, nil)
		first := syntheticAccessDirectory()
		first.Projects = append([]AccessProject{{ID: "removed", Dataset: "old-dataset"}}, first.Projects...)
		a.applyAccessDirectory(first)
		a.page, a.project = "知识", 1
		pane := a.state()
		pane.Loaded, pane.ListScroll.Y = true, 42
		pane.Selected = &Item{ID: "item", Dataset: "dataset", Body: "kept", BodyLoaded: true}
		next := syntheticAccessDirectory()
		next.Revision = 2
		a.applyAccessDirectory(next)
		if len(a.Config.Projects) != 1 || a.Config.Projects[0].ID != "p" || a.project != 0 || a.state() != pane {
			t.Fatal("catalog removal changed selected identity", a.Config.Projects, a.project)
		}
		if pane.Selected == nil || pane.Selected.Body != "kept" || pane.ListScroll.Y != 42 {
			t.Fatal("valid reading was lost")
		}
		if rows := a.searchCatalog(""); len(rows) != 1 || rows[0].Dataset != "dataset" {
			t.Fatal("obsolete search scope", rows)
		}
	})
	t.Run("rebind withdraws old body", func(t *testing.T) {
		a := New(Config{ServiceURL: "http://127.0.0.1:18999"}, nil)
		a.applyAccessDirectory(syntheticAccessDirectory())
		a.page = "知识"
		pane := a.state()
		pane.Selected = &Item{ID: "old", Dataset: "dataset", Body: "must disappear", BodyLoaded: true}
		next := syntheticAccessDirectory()
		next.Revision = 2
		next.Projects[0].Dataset = "replacement"
		a.applyAccessDirectory(next)
		if a.page != "总览" || pane.Selected != nil || a.Config.Projects[0].Dataset != "replacement" {
			t.Fatal("old association survived")
		}
	})
	t.Run("registry alone supplies code and common associations", func(t *testing.T) {
		a := New(Config{ServiceURL: "http://127.0.0.1:18999", Projects: []Project{{ID: "p", Dataset: "dataset", CodeDataset: "stale-code", CodeEnabled: true}}, Common: []Project{{ID: "stale", Dataset: "stale-common"}}}, nil)
		a.applyAccessDirectory(syntheticAccessDirectory())
		if len(a.Config.Common) != 0 || a.Config.Projects[0].CodeDataset != "" || a.Config.Projects[0].CodeEnabled {
			t.Fatal("local associations overrode registry")
		}
	})
}
