package desktop

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
	"github.com/zwhu/memlane/internal/store"
)

// Headless UI + real filesystem/SQLite; delay only the serialized UI callbacks.
func TestBackupVerificationDoesNotStrandCatalogueRead(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	root := t.TempDir()
	db, err := store.Open(filepath.Join(root, "memlane.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	receipt, err := db.Backup(ctx, root, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	app := New("0.0.4", nil)
	app.page = "设置"
	app.maintenanceSection = "backups"
	app.libraryContext = ctx
	updates := make(chan func(), 4)
	app.dispatch = func(fn func()) { updates <- fn }
	app.backups = BackupUI{Client: serviceipc.BackupClient{Root: root}, Items: []records.BackupReceipt{receipt}, Loaded: true}
	next := func() func() {
		t.Helper()
		select {
		case fn := <-updates:
			return fn
		case <-time.After(5 * time.Second):
			t.Fatal("backup catalogue remained blocked")
			return nil
		}
	}
	app.refreshBackups()
	old := next()
	app.verifyBackup(receipt.ID)
	next()()
	app.refreshBackups()
	old()
	next()()
	if app.backups.Loading || len(app.backups.Items) != 1 || app.backups.VerifiedID != receipt.ID {
		t.Fatal("backup verification stranded catalogue", app.backups)
	}
	tt := ui.NewTester(app.View, 1200, 780)
	if err := tt.Click("展开技术详情"); err != nil {
		t.Fatal("backup record disclosure missing after invalidated read", err)
	}
	if !tt.HasText("备份 · "+receipt.ID) || !tt.HasText("当前文件待校验") && app.backups.VerifiedAt == "" {
		t.Fatal("backup record not represented after invalidated read")
	}
}
