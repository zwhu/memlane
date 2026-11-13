package simpledesktop

import (
	"encoding/json"
	"github.com/egoist/mygo/ui"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestProjectCheckboxBundlesPermissionAndConfirmsUnknownWithoutRetry(t *testing.T) {
	a := settingsFixture()
	var directory AccessDirectory
	// Decode into a zero value so slices and nested maps cannot alias UI state.
	encoded, _ := json.Marshal(a.access.Directory)
	json.Unmarshal(encoded, &directory)
	var writes atomic.Int32
	var fail atomic.Bool
	var lastFields map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PUT" {
			writes.Add(1)
			var input struct {
				Revision int64          `json:"expected_revision"`
				Agent    string         `json:"agent_id"`
				Fields   map[string]any `json:"fields"`
			}
			json.NewDecoder(r.Body).Decode(&input)
			if r.URL.Path != "/v1/projects/p/overrides" || input.Agent != "agent" || input.Revision != directory.Revision || len(input.Fields) != 3 {
				t.Error("wrong target, stale revision or unrelated fields written")
			}
			enabled := input.Fields["access"] == "maintain"
			if input.Fields["capture"] != enabled || input.Fields["auto_process"] != enabled {
				t.Error("read/write permission did not include matching capture and processing")
			}
			lastFields = input.Fields
			o := directory.Projects[0].Overrides["agent"]
			for field, value := range input.Fields {
				switch field {
				case "access":
					v := value.(string)
					o.Access = &v
				case "capture":
					v := value.(bool)
					o.Capture = &v
				case "auto_process":
					v := value.(bool)
					o.AutoProcess = &v
				}
			}
			directory.Projects[0].Overrides["agent"] = o
			directory.Revision++
			directory.Projects[0].DesiredRevision = directory.Revision
			directory.Projects[0].Applied["agent"] = AccessApplied{Status: "verified", Revision: directory.Revision}
			if fail.Load() {
				w.WriteHeader(500)
				return
			}
		}
		json.NewEncoder(w).Encode(directory)
	}))
	defer server.Close()
	token := filepath.Join(t.TempDir(), "control.token")
	os.WriteFile(token, []byte(strings.Repeat("m", 40)), 0600)
	a.Config.ProjectControl = &ProjectControlConfig{URL: server.URL, ManagementTokenFile: token}
	updates := make(chan func(), 20)
	a.Update = func(fn func()) { updates <- fn }
	a.page = "设置"
	a.access.OpenProject = "p"
	a.settingsOrigin = &readingLocation{Page: "知识", ProjectID: "p"}
	tt := ui.NewTester(a.View, 1200, 820)
	for _, label := range []string{"允许读取知识", "允许读取知识", "允许写入和删除知识"} {
		clickProjectUI(t, tt, label)
		drainReading(t, updates, func() bool { return !a.access.Busy })
		tt.Frame()
		if a.access.CheckboxPending["p/agent"] != nil {
			t.Fatal("confirmed change stayed locked")
		}
	}
	if writes.Load() != 3 || lastFields["access"] != "maintain" || directory.Projects[0].Overrides["agent"].effective(directory.Defaults).Access != "maintain" {
		t.Fatal("checkbox values or read/write dependency lost")
	}
	fail.Store(true)
	clickProjectUI(t, tt, "允许写入和删除知识")
	drainReading(t, updates, func() bool { return !a.access.Busy })
	tt.Frame()
	if a.access.CheckboxPending["p/agent"] == nil {
		t.Fatal("unknown modification unlocked controls")
	}
	a.saveProjectCheckbox("p", "agent", "access", "none")
	if writes.Load() != 4 {
		t.Fatal("unknown operation resent")
	}
	clickProjectUI(t, tt, "重新核对")
	drainReading(t, updates, func() bool { return !a.access.Loading })
	if a.access.CheckboxPending["p/agent"] != nil || writes.Load() != 4 {
		t.Fatal("GET confirmation did not resolve original operation")
	}
}
