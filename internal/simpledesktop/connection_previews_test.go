package simpledesktop

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/controlplane"
)

func TestTraeHooksRepairPreviewAndConfirmedWrite(t *testing.T) {
	t.Run("explicit_zero_revision", func(t *testing.T) { testTraeHooksRepairPreviewAndConfirmedWrite(t, new(int64), 0) })
	t.Run("omitted_revision", func(t *testing.T) { testTraeHooksRepairPreviewAndConfirmedWrite(t, nil, 9) })
}

func testTraeHooksRepairPreviewAndConfirmedWrite(t *testing.T, revision *int64, expectedRevision int64) {
	t.Helper()
	id := "trae"
	cfg := Config{GenerationFile: filepath.Join(t.TempDir(), "generation.json"), ProjectControl: &ProjectControlConfig{
		URL: coldStartTestURL, ManagementTokenFile: p1TestTokenFile(t),
	}}
	var previews, writes atomic.Int32
	row := AgentConnection{ID: id, Kind: "trae_ide", Detected: true, Status: "configured_pending_new_session",
		NeedsHookRepair: true, ErrorCode: "trae_ide_unsupported_hook_events", Capture: &AgentCaptureState{State: "waiting_session"}}
	transport := queueTestTransport(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/connections/trae/repair-trae-hooks":
			var body struct {
				DryRun    bool   `json:"dry_run"`
				Operation string `json:"operation_id"`
				Revision  *int64 `json:"expected_revision"`
				SHA       string `json:"reviewed_sha"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			ops, err := loadGenerationOperationsFromDir(cfg.GenerationFile + ".ui-operations")
			if r.Method != http.MethodPost || err != nil {
				t.Errorf("invalid repair request: %s %v", r.Method, err)
			}
			if body.DryRun {
				var previewRevision *int64
				if previews.Add(1) == 2 {
					previewRevision = revision
				}
				if body.Operation != "" || body.SHA != "" || len(ops) != 0 {
					t.Error("repair preview recorded a mutation")
				}
				json.NewEncoder(w).Encode(struct {
					controlplane.TraeHooksRepairResult
					Revision *int64 `json:"revision,omitempty"`
				}{
					TraeHooksRepairResult: controlplane.TraeHooksRepairResult{
						Repairable: true, ReviewedSHA: "reviewed-hooks", Summary: "将 TRAE CN 连接迁移到四个受支持事件",
						RemovedEvents:             []string{"PostToolUseFailure", "SessionEnd"},
						RetainedEvents:            []string{"SessionStart", "UserPromptSubmit", "PostToolUse", "Stop"},
						PreservedThirdPartyGroups: 2, BackupPlan: "hooks.json.memlane-repair-backup",
					},
					Revision: previewRevision,
				})
				return
			}
			writes.Add(1)
			if body.Revision == nil || *body.Revision != expectedRevision || body.SHA != "reviewed-hooks" || len(ops) != 1 ||
				ops[0].Operation != body.Operation || ops[0].Revision != expectedRevision || ops[0].Kind != "connection_repair_trae_hooks" {
				t.Errorf("repair sent before durable reviewed operation: %+v, %v", body, ops)
			}
			json.NewEncoder(w).Encode(controlplane.TraeHooksRepairResult{Repaired: true, OperationID: body.Operation})
		case "/v1/connections":
			repaired := row
			repaired.NeedsHookRepair, repaired.ErrorCode = false, ""
			json.NewEncoder(w).Encode(ConnectionsDirectory{Revision: 8, Connections: []AgentConnection{repaired}})
		case "/v1/projects":
			json.NewEncoder(w).Encode(AccessDirectory{})
		default:
			t.Errorf("unexpected repair request %s %s", r.Method, r.URL.Path)
		}
	})
	a := New(cfg, nil)
	a.Client.HTTP = &http.Client{Transport: transport}
	a.integrations.Connections = ConnectionsDirectory{Revision: 9, Connections: []AgentConnection{row}}
	updates := make(chan func(), 8)
	a.Update = func(fn func()) { updates <- fn }
	tt := ui.NewTester(func(c *ui.Context) {
		a.agentConnectionRow(c, a.integrations.Connections.Connections[0])
	}, 1000, 550)
	if !tt.HasText("Hooks 需修复") || !tt.HasText("2. 在 TRAE 设置 → Hooks → 全局 中开启开关并确认安全提示。") ||
		!tt.HasText(formatControlConflictMessage(row.ErrorCode, "")) {
		t.Fatal("capture row hid repair or verification instructions")
	}
	clickProjectUI(t, tt, "复制排障指令交给 Agent")
	if prompt := tt.Clipboard(); prompt != agentTakeoverConnectionTroubleshootPrompt("trae_ide") || !strings.Contains(prompt, "--agent trae_ide --json") ||
		strings.Contains(prompt, "xattr") || strings.Contains(prompt, "知识") {
		t.Fatal("repair escape action copied an invalid prompt")
	}
	openPreview := func() {
		t.Helper()
		clickProjectUI(t, tt, "一键修复 TRAE Hooks")
		drainReading(t, updates, func() bool { return !a.integrations.Busy })
		tt.Frame()
		if writes.Load() != 0 {
			t.Fatal("repair preview applied changes without confirmation")
		}
	}
	openPreview()
	if got := a.integrations.TraeRepairPreview[id].Revision; got != 9 {
		t.Fatalf("missing preview revision did not fall back to the directory: %d", got)
	}
	for _, text := range []string{
		"将 TRAE CN 连接迁移到四个受支持事件",
		"移除事件 PostToolUseFailure、SessionEnd",
		"保留事件 SessionStart、UserPromptSubmit、PostToolUse、Stop",
		"保留 2 组第三方 Hooks",
		"备份原配置为 hooks.json.memlane-repair-backup",
		"确认修复", "取消",
	} {
		if !tt.HasText(text) {
			t.Fatalf("repair preview missing %q", text)
		}
	}
	clickProjectUI(t, tt, "取消")
	if tt.HasText("确认修复") || writes.Load() != 0 {
		t.Fatal("cancel did not dismiss preview without writing")
	}
	openPreview()
	if a.integrations.TraeRepairHasRevision[id] != (revision != nil) ||
		a.integrations.TraeRepairPreview[id].Revision != expectedRevision {
		t.Fatal("repair preview lost the revision or its presence")
	}
	clickProjectUI(t, tt, "确认修复")
	drainReading(t, updates, func() bool { return !a.integrations.Busy && !a.access.Loading })
	tt.Frame()
	ops, err := a.loadGenerationOperations()
	if _, present := a.integrations.TraeRepairHasRevision[id]; present {
		t.Fatal("repair revision presence survived apply")
	}
	if err != nil || len(ops) != 0 || previews.Load() != 2 || writes.Load() != 1 || tt.HasText("一键修复 TRAE Hooks") {
		t.Fatalf("unexpected confirmed repair result: ops=%v err=%v previews=%d writes=%d", ops, err, previews.Load(), writes.Load())
	}
}

func TestConnectionPreviewBindsPrepareAndActivateToReviewedDigest(t *testing.T) {
	for _, scenario := range []struct{ name, proposed, baseline, kind, status string }{
		{"matching", "reviewed", "original", "pi", "not_connected"},
		{"codex_incomplete_staging", "reviewed", "original", "codex", "staging_not_complete"},
		{"proposed_drift", "changed", "original", "pi", "not_connected"},
		{"baseline_drift", "reviewed", "changed", "pi", "not_connected"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			matches := scenario.proposed == "reviewed" && scenario.baseline == "original"
			cfg := Config{GenerationFile: filepath.Join(t.TempDir(), "generation.json"), ProjectControl: &ProjectControlConfig{
				URL: coldStartTestURL, ManagementTokenFile: p1TestTokenFile(t),
			}}
			var prepared, activated atomic.Bool
			var previewReads atomic.Int32
			row := AgentConnection{ID: scenario.kind, Kind: scenario.kind, Detected: true, Status: scenario.status}
			preview := controlplane.ConnectionPreview{OK: true, InstanceID: row.ID, Kind: row.Kind, Status: row.Status, ProposedSHA256: "reviewed", BaselineSHA256: "original", Revision: 7}
			if !connectionActionAllowed(row, "prepare") || !previewEligibleForConfirm(preview) {
				t.Fatalf("cannot resume preparation and confirmation for %s", row.Status)
			}
			if row.Status == "staging_not_complete" && connectionStatus(row) != "上次准备未完成" {
				t.Fatal("incomplete staging did not have a specific status label")
			}
			path := "/v1/connections/" + row.ID
			transport := queueTestTransport(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case path + "/preview":
					if r.Method != http.MethodGet {
						t.Error("connection preview was not read-only")
					}
					response := preview
					if previewReads.Add(1) == 2 && !matches {
						response.Status, response.Revision = "prepared", 8
						response.ProposedSHA256, response.BaselineSHA256 = scenario.proposed, scenario.baseline
					}
					json.NewEncoder(w).Encode(response)
				case path + "/prepare", path + "/activate":
					var body map[string]any
					json.NewDecoder(r.Body).Decode(&body)
					ops, err := loadGenerationOperationsFromDir(cfg.GenerationFile + ".ui-operations")
					if r.Method != http.MethodPost || err != nil || len(ops) != 1 || ops[0].Operation != body["operation_id"] {
						t.Error("action preceded durable operation", ops, err)
					}
					result := row
					revision := int64(8)
					if strings.HasSuffix(r.URL.Path, "/prepare") {
						if body["expected_revision"] != float64(7) {
							t.Error("prepare ignored the preview revision")
						}
						prepared.Store(true)
						result.Status = "prepared"
						result.Preview = &CodexConnectionPreview{ProposedSHA: scenario.proposed, BaselineSHA256: scenario.baseline}
					} else {
						if body["expected_revision"] != float64(8) || body["proposed_sha256"] != "reviewed" || !prepared.Load() {
							t.Error("activate did not use the verified proposal")
						}
						activated.Store(true)
						revision++
						result.Status = "configured_pending_new_session"
					}
					json.NewEncoder(w).Encode(map[string]any{"revision": revision, "connection": result})
				case "/v1/connections":
					json.NewEncoder(w).Encode(ConnectionsDirectory{})
				case "/v1/projects":
					json.NewEncoder(w).Encode(AccessDirectory{})
				default:
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
			})
			a := New(cfg, nil)
			a.Client.HTTP = &http.Client{Transport: transport}
			updates := make(chan func(), 8)
			a.Update = func(fn func()) { updates <- fn }
			a.integrations.NextReceiptCheck = time.Now().Add(time.Hour)
			a.confirmPrepareAndActivateConnection(row.ID, "reviewed")
			if a.integrations.Busy {
				t.Fatal("connection started without a reviewed preview")
			}
			a.integrations.PinnedAgentKinds = []string{row.Kind}
			tt := ui.NewTester(func(c *ui.Context) { a.agentConnectionRow(c, row) }, 1200, 820)
			clickProjectUI(t, tt, "连接 "+connectionName(row.Kind))
			drainReading(t, updates, func() bool { return !a.integrations.Busy })
			tt.Frame()
			if prepared.Load() || activated.Load() || !tt.HasText("一键确认接入") {
				t.Fatal("read-only preview changed host configuration or omitted confirmation")
			}
			clickProjectUI(t, tt, "一键确认接入")
			drainReading(t, updates, func() bool { return !a.integrations.Busy && !a.access.Loading })
			if !prepared.Load() || activated.Load() != matches {
				t.Fatalf("unexpected action sequence prepare=%v activate=%v", prepared.Load(), activated.Load())
			}
			if matches && (a.integrations.Error != "" || len(a.integrations.PendingOperations) != 0) {
				t.Fatalf("successful activation left an error or pending operation: %q, %v", a.integrations.Error, a.integrations.PendingOperations)
			}
			if !matches {
				refreshed := a.integrations.ConnectionPreviews[row.ID]
				if previewReads.Load() != 2 || refreshed.Revision != 8 || refreshed.BaselineSHA256 != scenario.baseline ||
					refreshed.ProposedSHA256 != scenario.proposed ||
					!strings.Contains(a.integrations.Error, "准备后的接入配置或宿主文件已变化，请重新确认连接") {
					t.Fatalf("changed preparation did not display the refreshed preview and drift error: %+v, %q", refreshed, a.integrations.Error)
				}
			}
		})
	}
}

func TestTraeSetupCaptureChecklistCollapsesWhenHealthy(t *testing.T) {
	for _, theme := range []struct {
		name string
		dark bool
	}{{"light", false}, {"dark", true}} {
		t.Run(theme.name, func(t *testing.T) {
			defer applyAppearance(false)
			a := New(Config{}, nil)
			row := AgentConnection{ID: "trae", Kind: "trae_ide", Detected: true, Status: "configured_pending_new_session",
				Capture: &AgentCaptureState{State: "awaiting_generation_activation"}}
			tt := ui.NewTester(func(c *ui.Context) {
				applyAppearance(c.Theme().Dark)
				a.agentConnectionRow(c, row)
			}, 1000, 400)
			tt.SetDark(theme.dark)
			const hooks = "2. 在 TRAE 设置 → Hooks → 全局 中开启开关并确认安全提示。"
			if !tt.HasText(hooks) || !tt.HasText("等待启用提炼后验证保存") || tt.HasText("采集状态读取失败") {
				t.Fatal("setup capture did not show calm status and verification steps")
			}
			row.Capture.State = "healthy"
			tt.Frame()
			if !tt.HasText("自动采集正常") || tt.HasText(hooks) || tt.HasText("收起步骤：TRAE CN") {
				t.Fatal("verified capture did not collapse the checklist")
			}
		})
	}
}

func TestAgentTakeoverPromptsUseMemoryAndAskBeforeChanges(t *testing.T) {
	fresh := agentTakeoverFreshInstallPrompt([]string{"codex", "trae_ide"})
	for _, test := range []struct {
		prompt   string
		required []string
	}{
		{fresh, []string{"Codex、TRAE CN", "严禁读取或输出任何 token/密钥", "给我看 --plan / --dry-run 预览并等我确认"}},
		{agentTakeoverBeta2RescuePrompt(), []string{"保留原记忆库", "不读凭据", "--dry-run --json 给我确认"}},
		{agentTakeoverConnectionTroubleshootPrompt("pi"), []string{"不手工编辑宿主配置文件或读取凭据", "先预览、经我确认再 --apply", "--agent pi --json"}},
		{agentTakeoverInstallCLIPrompt("TRAE CN"), []string{"记忆", "请勿读取或输出密钥", "等我确认后再执行"}},
	} {
		if strings.Contains(test.prompt, "知识") || strings.Contains(test.prompt, "xattr") {
			t.Fatalf("unexpected takeover prompt %q", test.prompt)
		}
		for _, required := range test.required {
			if !strings.Contains(test.prompt, required) {
				t.Errorf("takeover prompt must contain %q", required)
			}
		}
	}
}

func TestTraeHooksRepairWaitsForUnknownConnectionReview(t *testing.T) {
	a := New(Config{}, nil)
	row := AgentConnection{ID: "trae", Kind: "trae_ide", Detected: true, NeedsHookRepair: true}
	tt := ui.NewTester(func(c *ui.Context) { a.agentConnectionRow(c, row) }, 1200, 820)
	for _, status := range []string{"activation_unknown", "preparation_unknown", "staging_unknown", "agent_creation_unconfirmed"} {
		row.Status = status
		tt.Frame()
		if !tt.HasText("核对连接") || tt.HasText("一键修复 TRAE Hooks") {
			t.Fatalf("%s did not prioritize review: %v", status, tt.Texts())
		}
	}
	for _, status := range []string{"requires_review", "configured_pending_new_session"} {
		row.Status = status
		tt.Frame()
		if !tt.HasText("一键修复 TRAE Hooks") || slices.Contains(tt.Texts(), "核对连接") ||
			tt.HasText("核对连接状态") != (status == "requires_review") {
			t.Fatalf("%s did not offer repair: %v", status, tt.Texts())
		}
	}
}

func TestOpenCodePreparationRequiresFreshSecondConfirmation(t *testing.T) {
	root := t.TempDir()
	cfg := Config{GenerationFile: filepath.Join(root, "generation.json"), ProjectControl: beta3ControlConfig(t, root)}
	a := New(cfg, nil)
	const id = "opencode"
	initial := AgentConnection{ID: id, Kind: id, Status: "not_connected", Detected: true}
	prepared := initial
	prepared.Status = "prepared"
	prepared.Preview = &CodexConnectionPreview{ProposedSHA: "loader-digest"}
	connected := initial
	connected.Status = "configured_pending_new_session"
	var prepares, activates, previews atomic.Int32
	a.Client.HTTP = &http.Client{Transport: queueTestTransport(func(w http.ResponseWriter, r *http.Request) {
		var result any
		switch r.Method + " " + r.URL.Path {
		case "GET /v1/connections/opencode/preview":
			p := controlplane.ConnectionPreview{OK: true, InstanceID: id, Kind: id, Status: "not_connected",
				Revision: 1, PlanDigest: "baseline-digest", BaselineSHA256: "baseline-digest"}
			if prepares.Load() != 0 {
				p.Status, p.Revision, p.ProposedSHA256 = "prepared", 2, "loader-digest"
				p.PlanDigest = p.ProposedSHA256
			}
			previews.Add(1)
			result = p
		case "POST /v1/connections/opencode/prepare":
			prepares.Add(1)
			result = map[string]any{"revision": 2, "connection": prepared}
		case "POST /v1/connections/opencode/activate":
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil ||
				payload["expected_revision"] != float64(2) || payload["proposed_sha256"] != "loader-digest" {
				t.Errorf("activation ignored the fresh prepared preview: %v (%v)", payload, err)
			}
			activates.Add(1)
			result = map[string]any{"revision": 3, "connection": connected}
		case "GET /v1/connections":
			row, rev := prepared, int64(2)
			if activates.Load() > 0 {
				row, rev = connected, 3
			}
			result = ConnectionsDirectory{Revision: rev, Connections: []AgentConnection{row}}
		case "GET /v1/projects":
			result = AccessDirectory{}
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(result)
	})}
	updates := make(chan func(), 16)
	a.Update = func(fn func()) { updates <- fn }
	s := &a.integrations
	s.Connections.Connections = []AgentConnection{initial}
	tt := ui.NewTester(func(c *ui.Context) { a.agentConnectionRow(c, s.Connections.Connections[0]) }, 1200, 820)
	clickProjectUI(t, tt, "连接 OpenCode")
	drainReading(t, updates, func() bool { return !s.Busy })
	tt.Frame()
	if prepares.Load() != 0 || activates.Load() != 0 || !tt.HasText("准备连接") {
		t.Fatal("baseline-only preview did not require explicit preparation")
	}
	clickProjectUI(t, tt, "准备连接")
	drainReading(t, updates, func() bool { return !s.Busy })
	tt.Frame()
	if previews.Load() != 2 || prepares.Load() != 1 || activates.Load() != 0 || !tt.HasText("确认连接") {
		t.Fatal("preparation did not fetch a fresh preview for second confirmation")
	}
	clickProjectUI(t, tt, "确认连接")
	drainReading(t, updates, func() bool { return !s.Busy })
	if activates.Load() != 1 || prepares.Load() != 1 || s.Connections.Connections[0].Status != connected.Status {
		t.Fatal("second confirmation did not activate the prepared proposal exactly once")
	}
}

func TestConnectionConfirmRejectsNonEligiblePreviewStatuses(t *testing.T) {
	root := t.TempDir()
	a := New(Config{GenerationFile: filepath.Join(root, "generation.json"), ProjectControl: beta3ControlConfig(t, root)}, nil)
	var requests atomic.Int32
	a.Client.HTTP = &http.Client{Transport: queueTestTransport(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		t.Errorf("ineligible preview sent %s %s", r.Method, r.URL.Path)
		http.NotFound(w, r)
	})}
	a.Update = func(func()) {}
	for _, status := range []string{"configured_pending_new_session", "requires_review"} {
		preview := controlplane.ConnectionPreview{OK: true, InstanceID: "pi", Kind: "pi", Status: status, ProposedSHA256: "reviewed"}
		a.integrations.ConnectionPreviews = map[string]controlplane.ConnectionPreview{"pi": preview}
		a.confirmPrepareAndActivateConnection("pi", "reviewed")
		if a.integrations.Busy || requests.Load() != 0 || a.integrations.ConnectionPreviews["pi"].Status != status {
			t.Fatalf("confirmation accepted preview in %q", status)
		}
	}
}
