package settings

import (
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

func TestCaptureUsesSavedEvidenceInsteadOfNewSessionCount(t *testing.T) {
	a := newTestHost(Config{}, nil)
	a.integrations.Loaded = true
	for _, kind := range []string{"codex", "pi", "traex"} {
		a.integrations.Connections.Connections = append(a.integrations.Connections.Connections, AgentConnection{
			ID: kind, Kind: kind, Detected: true, Status: "configured_pending_new_session", NewSessions: 20,
			Capture: &AgentCaptureState{State: "waiting_message", Permission: "observed"}})
	}
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Padding(24).Children(func() { a.agentConnectionSettings(c) })
	}, 1000, 650)
	if tt.HasText("已连接") || tt.HasText("自动采集正常") || !tt.HasText("等待消息采集") {
		t.Fatal("a session binding was presented as a completed save")
	}
	a.integrations.Connections.Connections[0].Capture = &AgentCaptureState{State: "healthy", LastSavedAt: 1794931200}
	tt.Frame()
	if !tt.HasText("自动采集正常") {
		t.Fatal("verified save is not visible")
	}
	a.captureReadFailed()
	tt.Frame()
	if tt.HasText("自动采集正常") || !tt.HasText("采集状态读取失败") || a.integrations.Connections.Connections[0].Capture.LastSavedAt != 1794931200 {
		t.Fatal("read failure kept a green status or erased previous save evidence")
	}
}

func TestCodexCaptureConsentIsReviewedAndUnknownWriteIsOnlyReconciled(t *testing.T) {
	for _, action := range []string{"allow_capture", "defer_capture", "changed_review", "unknown_allow"} {
		t.Run(action, func(t *testing.T) {
			root := t.TempDir()
			token := filepath.Join(root, "token")
			if err := os.WriteFile(token, []byte(strings.Repeat("m", 40)), 0600); err != nil {
				t.Fatal(err)
			}
			var posts, stage atomic.Int32
			var operation string
			row := func() AgentConnection {
				capture := &AgentCaptureState{State: "needs_permission", Permission: "required", ReviewSHA: "reviewed"}
				if stage.Load() == 1 {
					capture.State, capture.Permission = "waiting_message", "granted"
					if action == "defer_capture" {
						capture.State, capture.Permission = "deferred", "required"
					}
				}
				return AgentConnection{ID: "codex", Kind: "codex", Detected: true, Status: "configured_pending_new_session", Capture: capture}
			}
			transport := queueTestTransport(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "POST" {
					posts.Add(1)
					var payload struct {
						SHA       string `json:"proposed_sha256"`
						Operation string `json:"operation_id"`
					}
					json.NewDecoder(r.Body).Decode(&payload)
					if payload.SHA != "reviewed" {
						t.Error("consent lost the reviewed hook identity")
					}
					operation = payload.Operation
					stage.Store(1)
					if action == "unknown_allow" {
						w.WriteHeader(503)
						return
					}
					json.NewEncoder(w).Encode(map[string]any{"revision": 2, "connection": row()})
					return
				}
				if strings.HasPrefix(r.URL.Path, "/v1/connections/operations/") {
					json.NewEncoder(w).Encode(map[string]any{
						"recorded": true, "status": "complete", "operation_id": operation, "instance_id": row().ID, "action": "allow_capture",
						"result": map[string]any{"connection": row()},
					})
				} else if r.URL.Path == "/v1/connections" {
					value := row()
					if action == "changed_review" {
						value.Capture.ReviewSHA = "changed"
					}
					json.NewEncoder(w).Encode(ConnectionsDirectory{Revision: 2, Connections: []AgentConnection{value}})
				} else {
					json.NewEncoder(w).Encode(AccessDirectory{})
				}
			})
			a := newTestHost(Config{GenerationFile: filepath.Join(root, "generation.json"),
				ProjectControl: &ProjectControlConfig{URL: coldStartTestURL, ManagementTokenFile: token}}, nil)
			updates := make(chan func(), 32)
			a.client.HTTP = &http.Client{Transport: transport}
			a.dispatch = func(fn func()) { updates <- fn }
			a.integrations = IntegrationState{Loaded: true, NextReceiptCheck: time.Now().Add(time.Hour),
				Connections: ConnectionsDirectory{Revision: 1, Connections: []AgentConnection{row()}}}
			tt := ui.NewTester(func(c *ui.Context) {
				ui.Column(c).Fill().Padding(24).Children(func() { a.agentConnectionSettings(c) })
			}, 1000, 650)
			clickProjectUI(t, tt, "允许自动采集：Codex")
			if posts.Load() != 0 || !tt.HasText("确认允许自动采集：Codex") || !tt.HasText("暂不开启自动采集：Codex") {
				t.Fatal("permission was changed before the explicit confirmation")
			}
			if action == "allow_capture" {
				fixtureImage(t, tt, "capture-consent")
			}
			label := "确认允许自动采集：Codex"
			if action == "defer_capture" {
				label = "暂不开启自动采集：Codex"
			}
			clickProjectUI(t, tt, label)
			drainReading(t, updates, func() bool { return !a.integrations.Busy })
			tt.Frame()
			if action == "changed_review" {
				if posts.Load() != 0 {
					t.Fatal("changed source was silently trusted")
				}
				return
			}
			if posts.Load() != 1 {
				t.Fatal("expected exactly one permission decision")
			}
			if action == "unknown_allow" {
				pending := a.integrations.pendingConnection("codex")
				if pending == nil || pending.Operation != operation {
					t.Fatal("unknown trust result lost its operation")
				}
				a.reviewConnectionOperation(*pending)
				drainReading(t, updates, func() bool { return !a.integrations.Busy && !a.integrations.Loading })
				if posts.Load() != 1 || a.integrations.pendingConnection("codex") != nil {
					t.Fatal("recovery repeated the permission write")
				}
			} else if action == "defer_capture" && connectionStatus(a.integrations.Connections.Connections[0]) != "自动采集未开启" {
				t.Fatal("decline was not displayed as an explicit user choice")
			}
		})
	}
}

func TestAntigravityHookErrorDisplayAndHealthyHide(t *testing.T) {
	a := newTestHost(Config{}, nil)
	a.integrations.Loaded = true
	a.integrations.Connections.Connections = []AgentConnection{
		{
			ID:       "antigravity-test",
			Kind:     "antigravity",
			Detected: true,
			Status:   "configured_pending_new_session",
			Capture:  &AgentCaptureState{State: "waiting_session", Permission: "observed"},
			LastHookError: &AgentHookError{
				At:        1795096000,
				Script:    "store-user-prompt.py",
				ErrorType: "NotReady",
				Reason:    "native_transcript_identity_required",
			},
		},
	}
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Padding(24).Children(func() { a.agentConnectionSettings(c) })
	}, 1000, 650)
	if !tt.HasText("最近一次 Hook 未记录：会话文件路径未通过校验（native_transcript_identity_required）") {
		t.Fatal("expected hook error diagnostic message to render when waiting_session")
	}
	a.integrations.Connections.Connections[0].Capture = &AgentCaptureState{State: "healthy", LastSavedAt: 1795096100}
	tt.Frame()
	if tt.HasText("最近一次 Hook 未记录：") {
		t.Fatal("expected hook error diagnostic message to hide once capture is healthy")
	}
}

func TestNeedsHookTrustSettingsAndOnboardingGuidance(t *testing.T) {
	root := t.TempDir()
	var refreshCount atomic.Int32
	codexRow := AgentConnection{
		ID:                 "agent-codex",
		Kind:               "codex",
		Detected:           true,
		AgentRegistered:    true,
		Status:             "configured_pending_new_session",
		Profile:            "/Users/example/.codex",
		NewSessions:        3,
		NeedsHookTrust:     true,
		UntrustedHookCount: 3,
		ModifiedHookCount:  3,
		Capture: &AgentCaptureState{
			State:       "needs_hook_trust",
			Permission:  "observed",
			Reason:      "host_hooks_untrusted",
			LastSavedAt: 1794931200,
		},
	}
	traexRow := AgentConnection{
		ID:                 "agent-traex",
		Kind:               "traex",
		Detected:           true,
		AgentRegistered:    true,
		Status:             "configured_pending_new_session",
		Profile:            "/Users/example/.trae",
		NeedsHookTrust:     true,
		UntrustedHookCount: 7,
		Capture: &AgentCaptureState{
			State:      "needs_hook_trust",
			Permission: "observed",
			Reason:     "host_hooks_untrusted",
		},
	}

	a := newTestHost(Config{
		GenerationFile: filepath.Join(root, "generation.json"),
		ProjectControl: beta3ControlConfig(t, root),
	}, nil)
	updates := make(chan func(), 32)
	a.dispatch = func(fn func()) { updates <- fn }
	a.client.HTTP = &http.Client{Transport: queueTestTransport(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/connections" {
			refreshCount.Add(1)
			if r.URL.Query().Get("force") != "1" {
				t.Errorf("RefreshConnections did not pass force=1: %s", r.URL.RequestURI())
			}
			trustedCodex := codexRow
			trustedCodex.NeedsHookTrust = false
			trustedCodex.UntrustedHookCount, trustedCodex.ModifiedHookCount = 0, 0
			trustedCodex.EnvironmentVerified = true
			trustedCodex.Capture = &AgentCaptureState{State: "healthy", Permission: "observed", LastSavedAt: 1794931200}
			json.NewEncoder(w).Encode(ConnectionsDirectory{Revision: 9, Connections: []AgentConnection{trustedCodex, traexRow}})
			return
		}
		http.NotFound(w, r)
	})}
	a.integrations = IntegrationState{
		Loaded:            true,
		ConnectionsLoaded: true,
		NextReceiptCheck:  time.Now().Add(time.Hour),
		Connections:       ConnectionsDirectory{Revision: 8, Connections: []AgentConnection{codexRow, traexRow}},
	}

	if captureVerifiedHealthy(codexRow) {
		t.Fatal("needs_hook_trust row must never report captureVerifiedHealthy")
	}
	if !shouldExpandChecklist(codexRow) || !shouldExpandChecklist(traexRow) {
		t.Fatal("needs_hook_trust rows must expand checklist by default")
	}
	if tone := agentConnectionBadgeTone(codexRow, nil, false, false, false); tone != "warn" {
		t.Fatalf("badge tone = %q, want warn", tone)
	}
	if connectionUnavailable(codexRow) || connectionUnavailable(traexRow) {
		t.Fatal("needs_hook_trust rows must stay visible")
	}

	tt := ui.NewTester(func(c *ui.Context) {
		applyAppearance(c.Theme().Dark)
		ui.Themed(c, homeTheme).Fill().FontWeight(400).Children(func() {
			ui.Column(c).Fill().Background(a.tokens().Bg).Padding(24).Children(func() { a.agentConnectionSettings(c) })
		})
	}, 1100, 900)
	t.Cleanup(func() { applyAppearance(false) })
	tt.SetDark(false)
	tt.Frame()
	fixtureImage(t, tt, "hook-trust-settings-light")
	tt.SetDark(true)
	tt.Advance(500 * time.Millisecond)
	fixtureImage(t, tt, "hook-trust-settings-dark")
	tt.SetDark(false)
	tt.Advance(500 * time.Millisecond)
	for _, want := range []string{
		"待信任 Hooks",
		"Codex 中有 6 条 Memlane Hook 待信任（3 条新增、3 条已变更），当前不会触发采集",
		"TRAE CLI 中有 7 条 Memlane Hook 待信任（7 条新增），当前不会触发采集",
		"Hooks need review",
		"Trust all and continue",
		"/hooks",
		"trust all",
		"复制 /hooks",
		"重新检测",
	} {
		if !tt.HasText(want) {
			t.Fatalf("settings agent connections missing %q; texts=%v", want, tt.Texts())
		}
	}
	if tt.HasText("自动采集正常") {
		t.Fatalf("needs_hook_trust must never show green 自动采集正常: %v", tt.Texts())
	}

	// Copy /hooks button writes "/hooks" to clipboard and shows brief "已复制" feedback.
	clickProjectUI(t, tt, "复制 /hooks：Codex")
	if got := tt.Clipboard(); got != "/hooks" {
		t.Fatalf("clipboard = %q, want /hooks", got)
	}
	if !tt.HasText("已复制") {
		t.Fatalf("missing brief 已复制 feedback after clicking 复制 /hooks: %v", tt.Texts())
	}
	tt.Advance(hookCommandCopyFeedbackDuration + 100*time.Millisecond)
	if tt.HasText("已复制") {
		t.Fatalf("已复制 feedback should expire after brief delay: %v", tt.Texts())
	}

	// Clicking 重新检测 refreshes with force=1 and flips Codex to healthy when trusted.
	clickProjectUI(t, tt, "重新检测自动采集：Codex")
	drainReading(t, updates, func() bool { return !a.integrations.Loading })
	tt.Frame()
	if refreshCount.Load() != 1 || !tt.HasText("自动采集正常") {
		t.Fatalf("expected force refresh to recover Codex to 自动采集正常: calls=%d texts=%v", refreshCount.Load(), tt.Texts())
	}

	// Onboarding step 3 renders the same amber status and 3-step /hooks card.
	a.initialSetup = true
	a.environment.InitialStep, a.environment.RuntimeStarted, a.environment.HostsLoaded = 3, true, true
	a.environment.SelectedAgents = []string{"traex"}
	if label, tone := a.onboardingConnectionStatus(traexRow); label != "待信任 Hooks" || tone != "warn" {
		t.Fatalf("onboarding status = (%q, %q), want (待信任 Hooks, warn)", label, tone)
	}
	onboardingTT := ui.NewTester(a.View, 1200, 900)
	onboardingTT.SetDark(false)
	onboardingTT.Advance(700 * time.Millisecond)
	fixtureImage(t, onboardingTT, "hook-trust-onboarding-light")
	onboardingTT.SetDark(true)
	onboardingTT.Advance(500 * time.Millisecond)
	fixtureImage(t, onboardingTT, "hook-trust-onboarding-dark")
	onboardingTT.SetDark(false)
	onboardingTT.Advance(500 * time.Millisecond)
	for _, want := range []string{
		"待信任 Hooks",
		"TRAE CLI 中有 7 条 Memlane Hook 待信任（7 条新增），当前不会触发采集",
		"Hooks need review",
		"Trust all and continue",
		"复制 /hooks",
		"重新检测",
	} {
		if !onboardingTT.HasText(want) {
			t.Fatalf("onboarding connect view missing %q; texts=%v", want, onboardingTT.Texts())
		}
	}
	clickProjectUI(t, onboardingTT, "复制 /hooks：TRAE CLI")
	if got := onboardingTT.Clipboard(); got != "/hooks" || !onboardingTT.HasText("已复制") {
		t.Fatalf("onboarding 复制 /hooks did not copy /hooks or show 已复制: clip=%q texts=%v", got, onboardingTT.Texts())
	}
}
