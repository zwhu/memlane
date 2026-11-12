package desktop

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/agents"
	"github.com/zwhu/memlane/internal/records"
)

func TestOfficialPluginInventoryIncludesDisconnectedAndRejectsUnreadable(t *testing.T) {
	root := t.TempDir()
	id := uuid.NewString()
	i := agents.Installation{Connection: records.Connection{ID: id, Agent: "codex", Enabled: false}, Options: agents.InstallOptions{ComputerLevel: true}}
	dir := filepath.Join(root, "agent-adapters", id)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(i)
	path := filepath.Join(dir, "installation.json")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	items, err := officialPluginLegacyInventory(root)
	if err != nil || len(items) != 1 || items[0].Connection.ID != id {
		t.Fatal(items, err)
	}
	if err := os.WriteFile(path, []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = officialPluginLegacyInventory(root); err == nil {
		t.Fatal("unreadable legacy inventory permitted installation")
	}
}

func TestOfficialPluginSettingsDoNotPromoteInstallIntoCaptureProof(t *testing.T) {
	a := New("test", nil)
	c := records.Connection{ID: uuid.NewString(), Agent: "codex", Enabled: true, Generation: 1}
	a.agents.OfficialPluginReceipts = map[string]agents.OfficialCodexPluginReceipt{c.ID: {State: "installed_pending_verification"}}
	a.agents.OfficialPluginGenerations = map[string]int64{c.ID: c.Generation}
	view := func() *ui.Tester {
		return ui.NewTester(func(ctx *ui.Context) { a.officialCodexPluginSettings(ctx, c) }, 660, 700)
	}
	tt := view()
	if !tt.HasText("插件已安装，等待信任、重载与采集验证") || tt.HasText("采集已可用") {
		t.Fatal("installation implied capture readiness")
	}
	a.agents.OfficialPluginReceipts[c.ID] = agents.OfficialCodexPluginReceipt{State: "installed_disabled"}
	if !view().HasText("插件已安装但已禁用，请在 Codex 中管理启用状态") {
		t.Fatal("disabled state lost")
	}
	a.agents.Installs = map[string]agents.Installation{c.ID: {CaptureConsent: &agents.CodexCaptureConsent{Enabled: true}}}
	if !view().HasText("停止旧自动记录") {
		t.Fatal("migration has no old-capture stop action")
	}
	a.agents.OfficialPluginHooks = map[string]agents.CodexPluginHookInspection{c.ID: {State: "hooks_ready"}}
	if !view().HasText("插件 hooks 已核对，服务与自动采集仍待验证") {
		t.Fatal("hook trust was not separated from real capture")
	}
	c.Generation++
	if view().HasText("插件 hooks 已核对，服务与自动采集仍待验证") {
		t.Fatal("old connection generation reused hook state")
	}
}
