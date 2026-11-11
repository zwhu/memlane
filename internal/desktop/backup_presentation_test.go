package desktop

import (
	"context"
	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/records"
	"testing"
)

func TestBackupMaintenanceKeepsRestoreConfirmationInFooter(t *testing.T) {
	a := New("test", nil)
	a.visualFixture = true
	a.page = "设置"
	a.maintenanceSection = "backups"
	a.libraryContext = context.Background()
	a.backups.Loaded = true
	a.backups.RestoreSelection = records.BackupReceipt{ID: "backup", CreatedAt: "2026-11-11T02:00:00Z", Schema: 26}
	tt := ui.NewTester(a.View, 1200, 820)
	for _, size := range [][2]int{{1200, 820}, {800, 720}} {
		tt.SetSize(size[0], size[1])
		for _, label := range []string{"取消恢复", "确认恢复此备份"} {
			n, ok := tt.Find(label)
			if !ok || n.X < 0 || n.X+n.W > float32(size[0]) || n.Y+n.H > float32(size[1]) || n.Y < float32(size[1]-64) {
				t.Fatalf("restore action outside footer %v %s %#v", size, label, n)
			}
		}
		if !tt.HasText("恢复整个本机记忆库") {
			t.Fatal("restore scope missing")
		}
	}
	a.backups.Busy = true
	a.backups.Notice = "正在停止服务并恢复；完成后 App 重新打开，服务保持停止…"
	tt.SetSize(800, 720)
	if !tt.HasText(a.backups.Notice) {
		t.Fatal("active restore lacks actual progress notice")
	}
}
