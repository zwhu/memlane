package desktop

// Headless UI and real SQLite/file recovery. Stop/worker and actual native
// relaunch are verified separately by the real App acceptance script.
import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/recoveryfiles"
	"github.com/zwhu/memlane/internal/serviceipc"
	"github.com/zwhu/memlane/internal/store"
)

func TestBackupRestorePreviewCancelConfirmationAndReceipt(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	root := t.TempDir()
	path := filepath.Join(root, "memlane.sqlite")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	var scope string
	if err = db.DB.QueryRow(`SELECT id FROM scopes WHERE kind='experience'`).Scan(&scope); err != nil {
		t.Fatal(err)
	}
	v, err := db.Apply(ctx, records.Mutation{Kind: "save", OperationID: uuid.NewString(), Memory: uuid.NewString(), Dataset: scope, Text: "synthetic backup UI v1"})
	if err != nil {
		t.Fatal(err)
	}
	backup, err := db.Backup(ctx, root, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Apply(ctx, records.Mutation{Kind: "edit", OperationID: uuid.NewString(), Memory: v.Memory, Dataset: scope, ExpectedRevision: 1, Text: "synthetic backup UI v2"})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	if err = serviceipc.MarkStopped(root); err != nil {
		t.Fatal(err)
	}
	prior, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	app := New("0.0.6", nil)
	app.experience.Distill = &ExperienceDistillUI{TaskID: "old-task"}
	app.organization.ExperienceDrafts = map[string]*ExperienceReviewDraft{"old-item": {Value: records.ExperienceReviewEdit{Text: "旧库未确认草稿"}}}
	updates := make(chan func(), 4)
	app.libraryContext = ctx
	app.dispatch = func(fn func()) { updates <- fn }
	app.backups = BackupUI{Client: serviceipc.BackupClient{Root: root}, Items: []records.BackupReceipt{backup}, Loaded: true}
	calls, reloads := 0, 0
	app.ConfigureBackupRecovery(func(ctx context.Context, backup, op string) (records.RestoreReceipt, error) {
		calls++
		return (recoveryfiles.RestoreManager{Root: root}).Restore(ctx, backup, op, store.PrepareRestoredSnapshot)
	}, func() { reloads++ })
	next := func() {
		t.Helper()
		select {
		case fn := <-updates:
			fn()
		case <-time.After(5 * time.Second):
			t.Fatal("restore UI update missing")
		}
	}
	app.previewBackupRestore(backup.ID)
	next()
	tester := ui.NewTester(app.backupSettings, 1000, 900)
	if !tester.HasText("恢复预览") {
		t.Fatal("verified preview absent")
	}
	if err = tester.Click("取消恢复"); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(prior, after) || calls != 0 || app.backups.RestoreSelection.ID != "" || app.experience.Distill == nil {
		t.Fatal("preview/cancel changed current knowledge", err, calls)
	}
	app.previewBackupRestore(backup.ID)
	next()
	tester = ui.NewTester(app.backupSettings, 1000, 900)
	if err = tester.Click("确认恢复此备份"); err != nil {
		t.Fatal(err)
	}
	next()
	if calls != 1 || reloads != 1 || app.backups.LastRestore.ID == "" || app.backups.Busy || app.runtimeStatus.Phase != "stopped" {
		t.Fatal("restore receipt/reload absent", calls, reloads, app.backups)
	}
	if app.HasExperiencePreview() || app.experiencePersistence != nil || len(app.organization.ExperienceDrafts) != 0 {
		t.Fatal("restored App retained old drafts or a shutdown flush")
	}
	if strings.Contains(app.backups.Error, "服务保持停止") {
		t.Fatal("failure text makes unproven stop claim")
	}
	// A genuine completion receipt enters the completion page, but a newly
	// opened catalogue must not replay that historic completion as a new event.
	completion := ui.NewTester(func(c *ui.Context) {
		selection := app.backups.RestoreSelection
		app.backups.RestoreSelection = records.BackupReceipt{}
		app.maintenanceRestoreSummary(c)
		app.backups.RestoreSelection = selection
	}, 1200, 820)
	if !completion.HasText("知识已恢复，服务保持停止") {
		t.Fatal("current actual restore lost its completion page")
	}
	fresh := New("test", nil)
	fresh.libraryContext = ctx
	fresh.backups.Client = serviceipc.BackupClient{Root: root}
	fresh.dispatch = func(fn func()) { updates <- fn }
	fresh.refreshBackups()
	next()
	if fresh.backups.LastRestore.ID != app.backups.LastRestore.ID {
		t.Fatal("history receipt missing from catalogue")
	}
	history := ui.NewTester(func(c *ui.Context) {
		if !fresh.maintenanceRestoreSummary(c) {
			fresh.backupBody(c, false)
		}
	}, 1200, 820)
	if history.HasText("知识已恢复，服务保持停止") || !history.HasText("备份与恢复") {
		t.Fatal("historic restore hijacked backup catalogue")
	}
	restored, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	current, err := restored.Current(ctx, v.Memory)
	if err != nil || current.Revision != 1 || current.Original != v.Original {
		t.Fatal("confirmed UI did not restore real SQLite", current, err)
	}
}
