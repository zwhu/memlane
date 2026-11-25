package settings

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestProjectCheckboxPreservesCaptureAndConfirmsUnknownWithoutRetry(t *testing.T) {
	a := settingsFixture()
	var directory AccessDirectory
	// Decode into a zero value so slices and nested maps cannot alias UI state.
	encoded, _ := json.Marshal(a.access.Directory)
	json.Unmarshal(encoded, &directory)
	capture, process := false, true
	override := directory.Projects[0].Overrides["agent"]
	override.Capture, override.AutoProcess = &capture, &process
	directory.Projects[0].Overrides["agent"] = override
	var writes atomic.Int32
	var fail atomic.Bool
	var lastFields map[string]any
	var fixtureLock sync.Mutex
	transport := queueTestTransport(func(w http.ResponseWriter, r *http.Request) {
		fixtureLock.Lock()
		defer fixtureLock.Unlock()
		if r.Method == "PUT" {
			writes.Add(1)
			var input struct {
				Revision int64          `json:"expected_revision"`
				Agent    string         `json:"agent_id"`
				Fields   map[string]any `json:"fields"`
			}
			json.NewDecoder(r.Body).Decode(&input)
			if r.URL.Path != "/v1/projects/p/overrides" || input.Agent != "agent" || input.Revision != directory.Revision || len(input.Fields) != 1 {
				t.Error("wrong target, stale revision or unrelated fields written")
			}
			value, ok := input.Fields["access"].(string)
			if !ok {
				t.Error("access update is missing")
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			lastFields = input.Fields
			o := directory.Projects[0].Overrides["agent"]
			o.Access = &value
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
	})
	token := filepath.Join(t.TempDir(), "control.token")
	os.WriteFile(token, []byte(strings.Repeat("m", 40)), 0600)
	a.config.ProjectControl = &ProjectControlConfig{URL: coldStartTestURL, ManagementTokenFile: token}
	a.client.HTTP = &http.Client{Transport: transport}
	updates := make(chan func(), 20)
	a.dispatch = func(fn func()) { updates <- fn }
	a.page = "设置"
	a.access.OpenProject = "p"
	tt := ui.NewTester(a.View, 1200, 820)
	if !tt.HasText("只读") {
		t.Fatal("missing initial tri-access value")
	}
	for _, nextVal := range []string{"none", "read", "maintain"} {
		a.saveProjectCheckbox("p", "agent", "access", nextVal)
		drainReading(t, updates, func() bool { return !a.access.Busy })
		tt.Frame()
		if a.access.CheckboxPending["p/agent"] != nil {
			t.Fatal("confirmed change stayed locked")
		}
	}
	fixtureLock.Lock()
	policy := directory.Projects[0].Overrides["agent"].Effective(directory.Defaults)
	valid := writes.Load() == 3 && lastFields["access"] == "maintain" && policy.Access == "maintain" && !policy.Capture && policy.AutoProcess
	fixtureLock.Unlock()
	if !valid || !tt.HasText("读写") {
		t.Fatal("access update changed an independent capture or processing setting")
	}
	fail.Store(true)
	a.saveProjectCheckbox("p", "agent", "access", "read")
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
