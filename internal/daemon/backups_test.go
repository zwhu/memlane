package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/recoveryfiles"
	"github.com/zwhu/memlane/internal/store"
)

func TestBackupOwnerCancelExactOperationAndNoPublication(t *testing.T) {
	root := t.TempDir()
	lifecycle, stop := context.WithCancel(context.Background())
	defer stop()
	s := &Server{config: Config{Root: root}, lifecycle: lifecycle}
	entered := make(chan struct{})
	s.backupCreate = func(ctx context.Context, id string) (records.BackupReceipt, error) {
		return (recoveryfiles.Manager{Root: root}).Create(ctx, id, func(ctx context.Context) (int, []byte, error) {
			close(entered)
			<-ctx.Done()
			return 0, nil, ctx.Err()
		})
	}
	id := uuid.NewString()
	finished := make(chan error, 1)
	go func() { _, err := s.createBackup(context.Background(), id); finished <- err }()
	<-entered
	if _, err := s.cancelBackup(context.Background(), uuid.NewString()); err == nil {
		t.Fatal("unknown operation reported cancellation")
	}
	status, err := s.backupStatus(context.Background(), id)
	if err != nil || status.State != "creating" {
		t.Fatal("unrelated cancellation affected active backup", status, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result, err := s.cancelBackup(ctx, id)
	if err != nil || result.State != "cancelled" || result.Receipt != nil {
		t.Fatal(result, err)
	}
	if err = <-finished; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(root, "backups", "manual", id)); !os.IsNotExist(err) {
		t.Fatal("cancelled backup was published", err)
	}
	entries, err := os.ReadDir(filepath.Join(root, "backups", "manual"))
	if err != nil || len(entries) != 0 {
		t.Fatal("cancelled backup leaked staging files", entries, err)
	}
}

func TestBackupOwnerCancelAfterPublicationReturnsReceipt(t *testing.T) {
	root := t.TempDir()
	lifecycle, stop := context.WithCancel(context.Background())
	defer stop()
	s := &Server{config: Config{Root: root}, lifecycle: lifecycle}
	db, err := store.Open(filepath.Join(root, "memory.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s.store = db
	id := uuid.NewString()
	receipt, err := s.createBackup(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.cancelBackup(context.Background(), id)
	if err != nil || result.State != "completed" || result.Receipt == nil || result.Receipt.SHA256 != receipt.SHA256 {
		t.Fatal(result, err)
	}
	// A fresh owner discovers committed receipt without an in-memory handle.
	restarted := &Server{config: Config{Root: root}, lifecycle: lifecycle}
	result, err = restarted.cancelBackup(context.Background(), id)
	if err != nil || result.State != "completed" || result.Receipt == nil || result.Receipt.ID != id {
		t.Fatal(result, err)
	}
}

func TestBackupOwnerFailedCaptureExplicitRetrySameID(t *testing.T) {
	root := t.TempDir()
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	db, err := store.Open(filepath.Join(root, "memory.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := &Server{config: Config{Root: root}, lifecycle: ctx, store: db}
	s.backupCreate = func(context.Context, string) (records.BackupReceipt, error) {
		return records.BackupReceipt{}, errors.New("explicit capture failure")
	}
	id := uuid.NewString()
	if _, err = s.createBackup(context.Background(), id); err == nil {
		t.Fatal("fault hidden")
	}
	s.backupCreate = nil
	receipt, err := s.createBackup(context.Background(), id)
	if err != nil || receipt.ID != id || receipt.Verification != "verified" {
		t.Fatal("explicit same-ID retry failed", receipt, err)
	}
	replay, err := s.createBackup(context.Background(), id)
	if err != nil || replay.SHA256 != receipt.SHA256 {
		t.Fatal("completed replay recaptured", replay, err)
	}
}
