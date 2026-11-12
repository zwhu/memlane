package desktop

import (
	"context"
	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/records"
	"testing"
)

func TestEnvironmentLayoutUsesRemainingContentWidth(t *testing.T) {
	for _, tt := range []struct {
		width         float32
		setup, narrow bool
	}{{1200, false, false}, {1280, false, false}, {1100, false, false}, {1200, true, false}, {1000, false, false}, {800, false, true}, {800, true, true}} {
		available, narrow := environmentContentLayout(tt.width, tt.setup)
		if narrow != tt.narrow || available < 0 {
			t.Fatalf("width=%v setup=%v available=%v narrow=%v", tt.width, tt.setup, available, narrow)
		}
		if !narrow && (available-28)/3 < 200 {
			t.Fatal("unreadable three-column cards")
		}
	}
	a := New("test", nil)
	a.visualFixture = true
	a.page = "设置"
	a.settingsSection = "environment"
	a.maintenanceSection = "components"
	tt := ui.NewTester(a.View, 800, 620)
	first, ok := tt.Find("原生运行依赖")
	if !ok {
		t.Fatal("database card missing")
	}
	second, ok := tt.Find("Cognee 引擎")
	if !ok || first.Y <= second.Y {
		t.Fatal("minimum window did not stack component cards", first, second)
	}
	tt.SetSize(1200, 820)
	first, _ = tt.Find("原生运行依赖")
	second, _ = tt.Find("Cognee 引擎")
	if first.Y != second.Y {
		t.Fatal("wide setting lost approved three cards", first, second)
	}
}

func TestMaintenanceTechnicalIDsRequireExplicitDisclosure(t *testing.T) {
	a := New("test", nil)
	a.visualFixture = true
	a.page = "设置"
	a.settingsSection = "environment"
	a.maintenanceSection = "backups"
	a.libraryContext = context.Background()
	a.backups.Loaded = true
	const id = "98ef59b2-9999-4444-aaaa-000000000001"
	a.backups.Items = []records.BackupReceipt{{ID: id, CreatedAt: "2026-11-11T01:00:00Z", Verification: "verified"}}
	tt := ui.NewTester(a.View, 1200, 820)
	if tt.HasText("备份 · " + id) {
		t.Fatal("backup ID exposed by default")
	}
	if err := tt.Click("展开技术详情"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("备份 · " + id) {
		t.Fatal("explicit disclosure lost full identity")
	}
	if err := tt.Click("收起技术详情"); err != nil {
		t.Fatal(err)
	}
	if tt.HasText("备份 · " + id) {
		t.Fatal("collapse did not hide identity")
	}
	a.maintenanceSection = "index"
	a.indexRebuild = records.IndexRebuild{ID: id, State: "partial_failed"}
	tt.SetSize(1200, 820)
	if tt.HasText("任务 · " + id) {
		t.Fatal("index ID exposed by default or inherited backup state")
	}
	if !tt.HasText("部分失败") || !tt.HasText("重试未完成项") {
		t.Fatal("safe disclosure hid actionable failure")
	}
	if err := tt.Click("展开技术详情"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("任务 · " + id) {
		t.Fatal("index detail lost exact ID")
	}
}
