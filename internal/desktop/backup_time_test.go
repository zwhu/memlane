package desktop

import (
	"context"
	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/records"
	"strings"
	"testing"
	"time"
)

func TestBackupTimesUseLocalMinutesWithoutChangingExactReceipt(t *testing.T) {
	zone := time.FixedZone("synthetic-local", 8*60*60)
	for _, tc := range []struct{ raw, want string }{{"2026-11-11T01:20:43.893682Z", "2026-11-11 09:20"}, {"2026-11-11T01:20:43.893682-04:00", "2026-11-11 13:20"}, {"2026-11-11 09:20:43", "2026-11-11 09:20"}, {"", "时间未记录"}, {"invalid", "时间待核对"}} {
		if got := backupDisplayTimeIn(tc.raw, zone); got != tc.want {
			t.Errorf("%q got %q want %q", tc.raw, got, tc.want)
		}
	}
	const exact = "2026-11-11T01:13:10.74455Z"
	a := New("test", nil)
	a.visualFixture = true
	a.libraryContext = context.Background()
	a.backups.Loaded = true
	a.backups.Items = []records.BackupReceipt{{ID: "synthetic-backup", CreatedAt: exact, Verification: "verified"}}
	tt := ui.NewTester(func(c *ui.Context) { a.backupBody(c, false) }, 800, 620)
	if tt.HasText("创建时间 · "+exact) || tt.HasText("选择备份 · "+exact) {
		t.Fatal("exact timestamp exposed by default")
	}
	if !tt.HasText("选择备份 · " + backupDisplayTime(exact)) {
		t.Fatal("local minute label missing")
	}
	if err := tt.Click("展开技术详情"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("创建时间 · "+exact) || a.backups.Items[0].CreatedAt != exact {
		t.Fatal("exact receipt lost or mutated")
	}
	a.backups.LastRestore = records.RestoreReceipt{ID: "synthetic-restore", CreatedAt: exact}
	tt.Frame()
	if !tt.HasText("最近完整恢复 · " + backupDisplayTime(exact)) {
		t.Fatal("restore date diverges")
	}
	// Default display must not expose the technical precision even in another zone.
	if strings.Contains(backupDisplayTime(exact), "T") || strings.Contains(backupDisplayTime(exact), "Z") {
		t.Fatal("ISO leaked")
	}
}
