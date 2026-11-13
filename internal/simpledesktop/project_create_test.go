package simpledesktop

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestProjectRenameUnknownConfirmationNeverWritesBindings(t *testing.T) {
	a := settingsFixture()
	directory := a.access.Directory
	var writes atomic.Int32
	var applied atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PUT" {
			writes.Add(1)
			var input map[string]any
			json.NewDecoder(r.Body).Decode(&input)
			if r.URL.Path != "/v1/projects/p/name" || len(input) != 2 || input["name"] != "新的名称" || input["expected_revision"] != float64(4) {
				t.Error("rename payload includes wrong target or bindings", input)
			}
			w.WriteHeader(500)
			return
		}
		if r.Method != "GET" || r.URL.Path != "/v1/projects" {
			t.Error("unexpected request", r.Method, r.URL.Path)
		}
		result := directory
		result.Projects = append([]AccessProject(nil), directory.Projects...)
		if applied.Load() {
			result.Projects[0].Name = "新的名称"
			result.Revision++
		}
		json.NewEncoder(w).Encode(result)
	}))
	defer server.Close()
	token := filepath.Join(t.TempDir(), "control.token")
	os.WriteFile(token, []byte(strings.Repeat("m", 40)), 0600)
	a.Config.ProjectControl = &ProjectControlConfig{URL: server.URL, ManagementTokenFile: token}
	updates := make(chan func(), 10)
	a.Update = func(fn func()) { updates <- fn }
	a.openProjectCreation("p")
	a.projectCreate.Name = "新的名称"
	// A stale local folder must never be submitted while editing.
	a.projectCreate.Folder = "/synthetic/do-not-link"
	a.submitProjectCreation()
	drainReading(t, updates, func() bool { return !a.projectCreate.Saving })
	if !a.projectCreate.Submitted || writes.Load() != 1 {
		t.Fatal("unknown rename not locked")
	}
	a.submitProjectCreation()
	a.confirmProjectCreation()
	drainReading(t, updates, func() bool { return !a.projectCreate.Saving })
	if !a.projectCreate.Open || !a.projectCreate.Submitted || writes.Load() != 1 {
		t.Fatal("old name falsely confirmed or rename resent")
	}
	applied.Store(true)
	a.confirmProjectCreation()
	drainReading(t, updates, func() bool { return !a.projectCreate.Saving })
	if a.projectCreate.Open || writes.Load() != 1 || a.Config.Projects[0].Name != "新的名称" {
		t.Fatal("GET confirmation failed")
	}
	if a.access.Directory.Projects[0].Dataset != "ds" || a.access.Directory.Projects[0].Aliases[0] != "/synthetic/memlane" {
		t.Fatal("rename changed bindings")
	}
}

func TestProjectCreationSuggestionUsesOneExistingDataset(t *testing.T) {
	for _, kind := range []string{"dataset", "project"} {
		t.Run(kind, func(t *testing.T) {
			a := settingsFixture()
			var writes atomic.Int32
			directory := a.access.Directory
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "POST" {
					writes.Add(1)
					var input map[string]any
					json.NewDecoder(r.Body).Decode(&input)
					if input["workspace"] != "/synthetic/new-project" || input["expected_revision"] != float64(4) {
						t.Error("wrong creation fields", input)
					}
					if kind == "dataset" && (r.URL.Path != "/v1/projects" || input["dataset_id"] != "existing" || len(input) != 4) {
						t.Error("dataset suggestion duplicated", input)
					}
					if kind == "project" && (r.URL.Path != "/v1/projects/existing/aliases" || len(input) != 2) {
						t.Error("project suggestion duplicated", input)
					}
					project := AccessProject{ID: "existing", Name: "已有项目", Dataset: "existing", Aliases: []string{"/synthetic/new-project"}}
					directory.Projects = append(directory.Projects, project)
					directory.Revision++
					json.NewEncoder(w).Encode(project)
					return
				}
				json.NewEncoder(w).Encode(directory)
			}))
			defer server.Close()
			token := filepath.Join(t.TempDir(), "control.token")
			os.WriteFile(token, []byte(strings.Repeat("m", 40)), 0600)
			a.Config.ProjectControl = &ProjectControlConfig{URL: server.URL, ManagementTokenFile: token}
			updates := make(chan func(), 10)
			a.Update = func(fn func()) { updates <- fn }
			a.projectCreate = projectCreateState{Open: true, Loaded: true, Name: "已有项目", Folder: "/synthetic/new-project", Selected: &projectSuggestion{ID: "existing", Name: "已有项目", Kind: kind}}
			a.submitProjectCreation()
			drainReading(t, updates, func() bool { return !a.projectCreate.Saving })
			if a.projectCreate.Open || writes.Load() != 1 {
				t.Fatal("creation not confirmed", a.projectCreate.Message)
			}
		})
	}
}
