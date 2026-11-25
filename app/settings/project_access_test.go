package settings

import (
	"context"
	"encoding/json"
	"net/http"
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
	a := newTestHost(Config{ServiceURL: "http://127.0.0.1:18999", Projects: []Project{{ID: "p", Name: "Project", Dataset: "dataset", CodeDataset: "code", CodeEnabled: true}}}, nil)
	first := syntheticAccessDirectory()
	first.Projects[0].CodeConfigured, first.Projects[0].CodeEnabled, first.Projects[0].CodeDataset = true, true, "code"
	a.ApplyAccessDirectory(first)
	initialAuth := a.authVersion
	discovered := syntheticAccessDirectory()
	discovered.Projects[0].CodeConfigured, discovered.Projects[0].CodeEnabled, discovered.Projects[0].CodeDataset = true, true, "code"
	discovered.Revision = 2
	discovered.Projects = append(discovered.Projects, AccessProject{ID: "new", Name: "New", Dataset: "new-dataset", Status: "verified", DesiredRevision: 2})
	a.ApplyAccessDirectory(discovered)
	if len(a.config.Projects) != 2 || !a.config.Projects[0].CodeEnabled || a.config.Projects[1].CodeEnabled {
		t.Fatal("code defaults or original association changed")
	}
	a.ApplyAccessDirectory(discovered)
	if a.authVersion != initialAuth {
		t.Fatal("unchanged permissions invalidated reading")
	}
	changed := syntheticAccessDirectory()
	changed.Revision = 3
	changed.Defaults.Access = "none"
	changed.Projects[0].DesiredRevision = 3
	a.ApplyAccessDirectory(changed)
	if a.authVersion != initialAuth+1 {
		t.Fatal("changed permissions did not invalidate reading")
	}

}

func TestProjectPolicyExplicitFalseAndUnconfirmedStatus(t *testing.T) {
	no := false
	none := "none"
	actual := (ProjectOverride{Access: &none, Capture: &no}).Effective(ProjectPolicy{Access: "maintain", Capture: true, AutoProcess: true})
	if actual.Capture || actual.Access != "none" || !actual.AutoProcess {
		t.Fatal("explicit false was treated as inheritance")
	}
	p := syntheticAccessDirectory().Projects[0]
	if p.SyncLabel() != "已生效" {
		t.Fatal(p.SyncLabel())
	}
	if p.SyncLabel(AccessAgent{ID: "new-agent"}) == "已生效" {
		t.Fatal("missing principal receipt was displayed as Effective")
	}
	p.Applied["codex"] = AccessApplied{Status: "verified", Revision: 0}
	if p.SyncLabel() == "已生效" {
		t.Fatal("accepted configuration mistaken for Effective ACL")
	}
}

func TestProjectDefaultsUIRejectsConcurrentRevisionWithoutLosingDraft(t *testing.T) {
	var requests atomic.Int32
	server := newTestHTTP(func(w http.ResponseWriter, r *http.Request) {
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
	})

	token := filepath.Join(t.TempDir(), "control.token")
	if err := os.WriteFile(token, []byte(strings.Repeat("m", 40)), 0600); err != nil {
		t.Fatal(err)
	}
	a := newTestHost(Config{ServiceURL: "http://127.0.0.1:18999", ProjectControl: &ProjectControlConfig{URL: server.URL, ManagementTokenFile: token}}, nil)
	a.client.HTTP = server.client
	a.ApplyAccessDirectory(syntheticAccessDirectory())
	a.access.Defaults.Capture = false
	a.access.DefaultsDirty = true
	newer := syntheticAccessDirectory()
	newer.Revision = 2
	newer.Defaults.Access = "read"
	a.ApplyAccessDirectory(newer)
	if a.access.DefaultsRevision != 1 || a.access.Defaults.Capture {
		t.Fatal("refresh replaced unsaved draft")
	}
	updates := make(chan func(), 10)
	a.dispatch = func(fn func()) { updates <- fn }
	tt := ui.NewTester(func(c *ui.Context) { ui.Scroll(c).Fill().Padding(20).Gap(10).Children(func() { a.accessSettings(c) }) }, 1004, 820)
	if !tt.HasText("项目默认规则") || !tt.HasText("可写并维护记忆（含删除）") {
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
	server := newTestHTTP(func(w http.ResponseWriter, r *http.Request) {
		if !recovered.Load() {
			w.WriteHeader(503)
			return
		}
		_ = json.NewEncoder(w).Encode(syntheticAccessDirectory())
	})

	token := filepath.Join(t.TempDir(), "control.token")
	_ = os.WriteFile(token, []byte(strings.Repeat("m", 40)), 0600)
	a := newTestHost(Config{ServiceURL: "http://127.0.0.1:18999", ProjectControl: &ProjectControlConfig{URL: server.URL, ManagementTokenFile: token}}, nil)
	a.client.HTTP = server.client
	a.ApplyAccessDirectory(syntheticAccessDirectory())
	updates := make(chan func(), 10)
	a.dispatch = func(fn func()) { updates <- fn }
	a.access.Message = "原保存结果待核对"
	a.RefreshProjectDirectory()
	drainReading(t, updates, func() bool { return !a.access.Loading })
	if len(a.config.Projects) != 1 || !a.access.Loaded || a.access.DirectoryError == "" {
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
	server := newTestHTTP(func(w http.ResponseWriter, r *http.Request) {
		var input map[string]any
		_ = json.NewDecoder(r.Body).Decode(&input)
		fields := input["fields"].(map[string]any)
		if fields["access"] != nil || fields["capture"] != false || input["agent_id"] != "codex" {
			t.Error("reset/false lost")
		}
		_ = json.NewEncoder(w).Encode(syntheticAccessDirectory())
	})

	token := filepath.Join(t.TempDir(), "control.token")
	_ = os.WriteFile(token, []byte(strings.Repeat("m", 40)), 0600)
	client := ProjectControlClient{HTTP: server.client, Config: ProjectControlConfig{URL: server.URL, ManagementTokenFile: token}}
	_, err := client.overrides(context.Background(), 1, "p", "codex", map[string]any{"access": nil, "capture": false})
	if err != nil {
		t.Fatal(err)
	}
}

func TestProjectControlConflictMessagesPreserveSafeCodesAndPhases(t *testing.T) {
	token := strings.Repeat("m", 40)
	for _, tc := range []struct {
		code, phase, hint string
	}{
		{"revision_conflict", "pre_mutation", "设置已被其他窗口或命令行更新，请刷新后再试"},
		{"reviewed_preview_required", "pre_mutation", "接入预览已过期，请重新预览确认后再继续"},
		{"host_configuration_changed", "pre_mutation", "宿主配置文件已变化，请刷新后核对"},
		{"host_configuration_changed", "post_mutation", "写入后检测到宿主配置文件变化，需人工核对"},
		{"codex_configuration_changed", "pre_mutation", "Codex 配置已变动，请刷新后核对"},
		{"trae_ide_hook_changed_requires_review", "", "TRAE CN Hooks 配置与记录不一致，请使用「一键修复 TRAE Hooks」或核对连接"},
		{"trae_ide_unsupported_hook_events", "", "TRAE CN Hooks 包含旧版不兼容事件，请点击「一键修复 TRAE Hooks」"},
		{"connection_operation_ambiguous_requires_review", "post_mutation", "该次历史连接操作存在写入风险，需人工核对后处理"},
		{"host_executable_changed", "", "Agent 应用版本已更新，请点击「核对连接」确认"},
		{"project_workspace_conflict", "", "该项目目录已关联到其他项目，请检查目录后重试"},
		{"project_binding_conflict", "", "该项目目录已关联到其他项目，请检查目录后重试"},
		{"project_already_exists", "", "同名项目已存在，请更换名称后重试"},
		{"dataset_already_linked", "", "该记忆库已关联到其他项目，请刷新后核对"},
		{"new_conflict", "pre_mutation", "操作在写入前已拦截，请刷新核对原操作"},
		{"new_conflict", "post_mutation", "写入后状态待核对，请人工核对原操作"},
		{"new_conflict", "", "操作存在冲突，请刷新核对原操作"},
		{"", "", "设置已变化或存在冲突，请刷新核对原操作"},
	} {
		t.Run(tc.code+"/"+tc.phase, func(t *testing.T) {
			transport := queueTestTransport(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusConflict)
				json.NewEncoder(w).Encode(map[string]string{"code": tc.code, "error": "untrusted raw text /" + token, "phase": tc.phase})
			})
			tokenFile := filepath.Join(t.TempDir(), "token")
			if err := os.WriteFile(tokenFile, []byte(token), 0600); err != nil {
				t.Fatal(err)
			}
			client := ProjectControlClient{HTTP: &http.Client{Transport: transport}, Config: ProjectControlConfig{URL: coldStartTestURL, ManagementTokenFile: tokenFile}}
			err := client.request(context.Background(), http.MethodPost, "/v1/projects", nil, nil)
			response, ok := err.(*ControlResponseError)
			want := tc.hint
			if tc.code != "" {
				want += "（" + tc.code + "）"
			}
			if !ok || response.Code != tc.code || response.Phase != tc.phase || response.Error() != want || ControlUserError(err) != want {
				t.Fatalf("unexpected control conflict %+v, want %q", err, want)
			}
		})
	}
	if code, phase := parseControlError([]byte(`{"error":"`+token+`","phase":"`+token+`"}`), token); code != "" || phase != "" {
		t.Fatal("identifier-shaped credential escaped sanitization")
	}
	if code, phase := parseControlError([]byte(`{"error":"server_conflict","phase":"post_mutation"}`), token); code != "server_conflict" || phase != "post_mutation" {
		t.Fatal("error field was not accepted as a fallback")
	}
}
