package simpledesktop

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
	a := New(Config{}, nil)
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
			a := New(Config{GenerationFile: filepath.Join(root, "generation.json"),
				ProjectControl: &ProjectControlConfig{URL: coldStartTestURL, ManagementTokenFile: token}}, nil)
			updates := make(chan func(), 32)
			a.Client.HTTP = &http.Client{Transport: transport}
			a.Update = func(fn func()) { updates <- fn }
			a.integrations = integrationState{Loaded: true, NextReceiptCheck: time.Now().Add(time.Hour),
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
	a := New(Config{}, nil)
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
