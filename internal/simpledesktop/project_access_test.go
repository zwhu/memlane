package simpledesktop

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func syntheticAccessDirectory() AccessDirectory {
	return AccessDirectory{Revision: 1, Defaults: ProjectPolicy{Access: "maintain", Capture: true, AutoProcess: true}, Agents: []AccessAgent{{ID: "codex", Name: "Codex"}}, Projects: []AccessProject{{ID: "p", Name: "Project", Dataset: "dataset", Aliases: []string{"/synthetic/project"}, Status: "verified", DesiredRevision: 1, Applied: map[string]AccessApplied{"codex": {Status: "verified", Revision: 1}}}}}
}

func TestProjectDiscoveryKeepsReadingAndPolicyChangeInvalidates(t *testing.T) {
	a := New(Config{ServiceURL: "http://127.0.0.1:18999", Projects: []Project{{ID: "p", Name: "Project", Dataset: "dataset", CodeDataset: "code", CodeEnabled: true}}}, nil)
	first := syntheticAccessDirectory()
	a.applyAccessDirectory(first)
	a.page = "知识"
	s := a.state()
	s.Loaded = true
	s.ListScroll.Y = 45
	selected := Item{ID: "knowledge", Dataset: "dataset", Kind: "知识", Body: "confirmed body", BodyLoaded: true}
	s.Selected = &selected
	s.History = []Item{selected}
	discovered := syntheticAccessDirectory()
	discovered.Revision = 2
	discovered.Projects = append(discovered.Projects, AccessProject{ID: "new", Name: "New", Dataset: "new-dataset", Status: "verified", DesiredRevision: 2})
	a.applyAccessDirectory(discovered)
	if s.Selected == nil || s.Selected.Body != "confirmed body" || s.ListScroll.Y != 45 || len(s.History) != 1 || a.project != 0 {
		t.Fatal("new project discovery disrupted reading")
	}
	if len(a.Config.Projects) != 2 || !a.Config.Projects[0].CodeEnabled || a.Config.Projects[1].CodeEnabled {
		t.Fatal("code defaults or original association changed")
	}
	a.applyAccessDirectory(discovered)
	if s.Selected == nil || s.Selected.Body == "" {
		t.Fatal("unchanged refresh removed body")
	}
	changed := syntheticAccessDirectory()
	changed.Revision = 3
	changed.Defaults.Access = "none"
	changed.Projects[0].DesiredRevision = 3
	a.applyAccessDirectory(changed)
	if s.Selected != nil && s.Selected.Body != "" {
		t.Fatal("changed policy retained stale copyable body")
	}
	if len(s.History) != 0 {
		t.Fatal("changed policy retained reading history body")
	}
}

func TestProjectPolicyExplicitFalseAndUnconfirmedStatus(t *testing.T) {
	no := false
	none := "none"
	actual := (ProjectOverride{Access: &none, Capture: &no}).effective(ProjectPolicy{Access: "maintain", Capture: true, AutoProcess: true})
	if actual.Capture || actual.Access != "none" || !actual.AutoProcess {
		t.Fatal("explicit false was treated as inheritance")
	}
	p := syntheticAccessDirectory().Projects[0]
	if p.syncLabel() != "已生效" {
		t.Fatal(p.syncLabel())
	}
	if p.syncLabel(AccessAgent{ID: "new-agent"}) == "已生效" {
		t.Fatal("missing principal receipt was displayed as effective")
	}
	p.Applied["codex"] = AccessApplied{Status: "verified", Revision: 0}
	if p.syncLabel() == "已生效" {
		t.Fatal("accepted configuration mistaken for effective ACL")
	}
}

func TestProjectDefaultsUIRejectsConcurrentRevisionWithoutLosingDraft(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("Authorization") != "Bearer "+strings.Repeat("m", 40) || r.Header.Get("X-Api-Key") != "" {
			t.Error("wrong control credential protocol")
		}
		if r.Method != "PUT" || r.URL.Path != "/v1/defaults" {
			t.Error("unexpected retry/directory request")
		}
		var input struct {
			Revision int64         `json:"expected_revision"`
			Fields   ProjectPolicy `json:"fields"`
		}
		if json.NewDecoder(r.Body).Decode(&input) != nil || input.Revision != 1 || input.Fields.Capture {
			t.Error("stale draft revision/false not preserved")
		}
		w.WriteHeader(409)
	}))
	defer server.Close()
	token := filepath.Join(t.TempDir(), "control.token")
	if err := os.WriteFile(token, []byte(strings.Repeat("m", 40)), 0600); err != nil {
		t.Fatal(err)
	}
	a := New(Config{ServiceURL: "http://127.0.0.1:18999", ProjectControl: &ProjectControlConfig{URL: server.URL, ManagementTokenFile: token}}, nil)
	a.applyAccessDirectory(syntheticAccessDirectory())
	a.access.Defaults.Capture = false
	a.access.DefaultsDirty = true
	newer := syntheticAccessDirectory()
	newer.Revision = 2
	newer.Defaults.Access = "read"
	a.applyAccessDirectory(newer)
	if a.access.DefaultsRevision != 1 || a.access.Defaults.Capture {
		t.Fatal("refresh replaced unsaved draft")
	}
	updates := make(chan func(), 10)
	a.Update = func(fn func()) { updates <- fn }
	tt := ui.NewTester(func(c *ui.Context) { ui.Scroll(c).Fill().Padding(20).Gap(10).Children(func() { a.accessSettings(c) }) }, 1004, 820)
	if !tt.HasText("项目默认规则") || !tt.HasText("可写并维护知识（含删除）") {
		t.Fatal("policy semantics missing")
	}
	if err := tt.Click("保存默认规则"); err != nil {
		t.Fatal(err)
	}
	drainReading(t, updates, func() bool { return !a.access.Busy })
	if requests.Load() != 1 || !a.access.DefaultsDirty || a.access.Defaults.Capture || !strings.Contains(a.access.Message, "设置已变化") {
		t.Fatal("concurrent save retried or draft lost")
	}
}

func TestProjectDirectoryFailureKeepsCatalog(t *testing.T) {
	var recovered atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !recovered.Load() {
			w.WriteHeader(503)
			return
		}
		_ = json.NewEncoder(w).Encode(syntheticAccessDirectory())
	}))
	defer server.Close()
	token := filepath.Join(t.TempDir(), "control.token")
	_ = os.WriteFile(token, []byte(strings.Repeat("m", 40)), 0600)
	a := New(Config{ServiceURL: "http://127.0.0.1:18999", ProjectControl: &ProjectControlConfig{URL: server.URL, ManagementTokenFile: token}}, nil)
	a.applyAccessDirectory(syntheticAccessDirectory())
	updates := make(chan func(), 10)
	a.Update = func(fn func()) { updates <- fn }
	a.access.Message = "原保存结果待核对"
	a.RefreshProjectDirectory()
	drainReading(t, updates, func() bool { return !a.access.Loading })
	if len(a.Config.Projects) != 1 || !a.access.Loaded || a.access.DirectoryError == "" {
		t.Fatal("failed refresh cleared known project")
	}
	if time.Until(a.access.Next) > 16*time.Second || time.Until(a.access.Next) < 0 {
		t.Fatal("unbounded directory retry")
	}
	recovered.Store(true)
	a.RefreshProjectDirectory()
	drainReading(t, updates, func() bool { return !a.access.Loading })
	if a.access.DirectoryError != "" || a.access.Message != "原保存结果待核对" {
		t.Fatal("recovery kept an obsolete directory error or lost an unknown write result")
	}
}

func TestProjectControlOverridesSendNullResetAndFalse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input map[string]any
		_ = json.NewDecoder(r.Body).Decode(&input)
		fields := input["fields"].(map[string]any)
		if fields["access"] != nil || fields["capture"] != false || input["agent_id"] != "codex" {
			t.Error("reset/false lost")
		}
		_ = json.NewEncoder(w).Encode(syntheticAccessDirectory())
	}))
	defer server.Close()
	token := filepath.Join(t.TempDir(), "control.token")
	_ = os.WriteFile(token, []byte(strings.Repeat("m", 40)), 0600)
	client := ProjectControlClient{Config: ProjectControlConfig{URL: server.URL, ManagementTokenFile: token}}
	_, err := client.overrides(context.Background(), 1, "p", "codex", map[string]any{"access": nil, "capture": false})
	if err != nil {
		t.Fatal(err)
	}
}
