package desktop

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/agents"
	"github.com/zwhu/memlane/internal/records"
)

func TestCaptureSettingsDoNotTurnToolConnectionIntoCollectionProof(t *testing.T) {
	a := New("test", nil)
	home := t.TempDir()
	profile := filepath.Join(home, ".codex")
	if err := os.Mkdir(profile, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", profile)
	a.agents.Home = home
	connection := records.Connection{ID: uuid.NewString(), Agent: "codex", Enabled: true, Generation: 1}
	i := agents.Installation{Connection: connection, Options: agents.InstallOptions{ComputerLevel: true, CodexDirectory: profile}, ProtocolGeneration: 1, ProtocolVerifiedAt: "synthetic protocol receipt", ProtocolScopeDigest: records.MergeDigest([]any{connection.Project, connection.AllowedScopes})}
	a.agents.Installs = map[string]agents.Installation{connection.ID: i}
	view := func() *ui.Tester {
		return ui.NewTester(func(c *ui.Context) { a.codexCaptureSettings(c, connection) }, 660, 700)
	}
	tt := view()
	if !tt.HasText("未开启自动记录") || !tt.HasText("授权并开启自动记录") {
		t.Fatal("tool connection implied authorized collection")
	}
	i.CaptureConsent = &agents.CodexCaptureConsent{ID: "current-consent", Enabled: true}
	i.CaptureHooks = &agents.CodexCaptureHooks{}
	a.agents.Installs[connection.ID] = i
	a.agents.CaptureStatus = map[string]*agents.CodexCaptureStatus{connection.ID: {Consent: "prior-consent", Receipt: agents.CodexCaptureReceipt{State: "retained_pending_assignment", LocalRetained: true}}}
	tt = view()
	if !tt.HasText("等待 Codex 确认与重载") || tt.HasText("已收到对话 · 待归属") {
		t.Fatal("old authorization event implied current readiness")
	}
	a.agents.CaptureStatus[connection.ID].Consent = "current-consent"
	tt = view()
	if !tt.HasText("已收到对话 · 待归属") || !tt.HasText("原始记录已在本机保留，尚未取得项目保存回执。") || !tt.HasText("查看待发送原文") {
		t.Fatal("local retention was misrepresented as project delivery")
	}
	connection.Enabled = false
	tt = view()
	if !tt.HasText("连接已断开") || tt.HasText("已收到对话 · 待归属") {
		t.Fatal("disconnected connection implied ongoing collection")
	}
}
