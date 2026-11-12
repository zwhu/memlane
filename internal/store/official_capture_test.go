package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

func captureLedgerFixture(t *testing.T) (*Store, string, records.OfficialCaptureIdentity) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "memlane.sqlite")
	s, err := Open(path)
	mustMerge(t, err)
	t.Cleanup(func() { s.Close() })
	library, err := s.CogneeLibraryIdentity(context.Background())
	mustMerge(t, err)
	return s, path, records.OfficialCaptureIdentity{Operation: strings.Repeat("a", 64), Library: library, GrantDigest: strings.Repeat("b", 64), Instance: uuid.NewString(), Dataset: uuid.NewString(), Session: "pi-" + strings.Repeat("c", 48), Kind: "qa", PayloadDigest: strings.Repeat("d", 64)}
}

func TestOfficialCaptureLedgerOneDispatchAndNoTranscriptMirror(t *testing.T) {
	s, _, q := captureLedgerFixture(t)
	ctx := context.Background()
	var winners atomic.Int64
	var wait sync.WaitGroup
	for i := 0; i < 12; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			row, claimed, err := s.ClaimOfficialCapture(ctx, q, false)
			if err != nil {
				t.Error(err)
				return
			}
			if row.State != "dispatching" {
				t.Error(row)
			}
			if claimed {
				winners.Add(1)
			}
		}()
	}
	wait.Wait()
	if winners.Load() != 1 {
		t.Fatal("duplicate dispatch claimed", winners.Load())
	}
	id := uuid.NewString()
	mustMerge(t, s.SealOfficialCapture(ctx, q, id))
	mustMerge(t, s.SealOfficialCapture(ctx, q, id))
	row, claimed, err := s.ClaimOfficialCapture(ctx, q, false)
	mustMerge(t, err)
	if claimed || row.State != "saved" || row.OfficialID != id {
		t.Fatal(row, claimed)
	}
	if s.SealOfficialCapture(ctx, q, uuid.NewString()) == nil {
		t.Fatal("confirmed result replaced")
	}
	for _, table := range []string{"memories", "versions", "operations", "jobs"} {
		var n int
		mustMerge(t, s.DB.QueryRow(`SELECT count(*) FROM `+table).Scan(&n))
		if n != 0 {
			t.Fatal("capture created duplicate content", table, n)
		}
	}
	var columns string
	mustMerge(t, s.DB.QueryRow(`SELECT sql FROM sqlite_master WHERE name='official_capture_receipts'`).Scan(&columns))
	for _, forbidden := range []string{"question", "answer", "method_params", "token", "credential"} {
		if strings.Contains(columns, forbidden) {
			t.Fatal("transcript or credential column", forbidden)
		}
	}
}

func TestOfficialCaptureLedgerUnknownSurvivesReopenAndConflictsFailClosed(t *testing.T) {
	s, path, q := captureLedgerFixture(t)
	ctx := context.Background()
	_, claimed, err := s.ClaimOfficialCapture(ctx, q, false)
	mustMerge(t, err)
	if !claimed {
		t.Fatal("no initial claim")
	}
	mustMerge(t, s.Close())
	reopened, err := Open(path)
	mustMerge(t, err)
	defer reopened.Close()
	row, claimed, err := reopened.ClaimOfficialCapture(ctx, q, false)
	mustMerge(t, err)
	if claimed || row.State != "dispatching" {
		t.Fatal("unknown outcome became resend permission", row, claimed)
	}
	for _, field := range []string{"grant", "payload", "instance", "dataset", "session", "kind"} {
		changed := q
		switch field {
		case "grant":
			changed.GrantDigest = strings.Repeat("e", 64)
		case "payload":
			changed.PayloadDigest = strings.Repeat("e", 64)
		case "instance":
			changed.Instance = uuid.NewString()
		case "dataset":
			changed.Dataset = uuid.NewString()
		case "session":
			changed.Session = "pi-" + strings.Repeat("e", 48)
		case "kind":
			changed.Kind = "trace"
		}
		if _, _, err := reopened.ClaimOfficialCapture(ctx, changed, false); !errors.Is(err, ErrConflict) {
			t.Fatal("changed dispatch identity accepted", field, err)
		}
		if _, err := reopened.OfficialCaptureReceipt(ctx, changed); !errors.Is(err, ErrConflict) {
			t.Fatal("changed receipt identity accepted", field, err)
		}
	}
	mustMerge(t, reopened.SealOfficialCapture(ctx, q, uuid.NewString()))
}

func TestOfficialCaptureLedgerFilteredAndLibraryChange(t *testing.T) {
	s, _, q := captureLedgerFixture(t)
	ctx := context.Background()
	row, claimed, err := s.ClaimOfficialCapture(ctx, q, true)
	mustMerge(t, err)
	if claimed || row.State != "policy_filtered" {
		t.Fatal("filtered content scheduled for dispatch")
	}
	if _, _, err := s.ClaimOfficialCapture(ctx, q, false); !errors.Is(err, ErrConflict) {
		t.Fatal("filtered entry became network write")
	}
	if s.SealOfficialCapture(ctx, q, uuid.NewString()) == nil {
		t.Fatal("filtered entry given fabricated official receipt")
	}
	_, err = s.DB.Exec(`UPDATE cognee_library_identity SET identity=? WHERE id=1`, uuid.NewString())
	mustMerge(t, err)
	if _, _, err := s.ClaimOfficialCapture(ctx, q, true); err == nil {
		t.Fatal("cross-library claim accepted")
	}
	if _, err := s.OfficialCaptureReceipt(ctx, q); err == nil {
		t.Fatal("cross-library receipt accepted")
	}
}

func TestOfficialCaptureLedgerSchema32UpgradeAndBackupRestore(t *testing.T) {
	s, path, q := captureLedgerFixture(t)
	ctx := context.Background()
	_, err := s.DB.Exec(`DROP TABLE official_code_scopes;DROP TABLE coding_memory_grants; DROP TABLE pi_capture_read_grants; DROP TABLE pi_capture_consents; DROP TABLE official_capture_receipts; PRAGMA user_version=32`)
	mustMerge(t, err)
	mustMerge(t, s.Close())
	upgraded, err := Open(path)
	mustMerge(t, err)
	defer upgraded.Close()
	if upgraded.Schema != SchemaVersion {
		t.Fatal("schema did not upgrade", upgraded.Schema)
	}
	backups, err := filepath.Glob(filepath.Join(filepath.Dir(path), "backups", "schema-v32-*.sqlite"))
	mustMerge(t, err)
	if len(backups) != 1 {
		t.Fatal("schema migration backup missing", backups)
	}
	_, claimed, err := upgraded.ClaimOfficialCapture(ctx, q, false)
	mustMerge(t, err)
	if !claimed {
		t.Fatal("claim missing")
	}
	_, snapshot, err := upgraded.snapshot(ctx)
	mustMerge(t, err)
	prepared, _, err := PrepareRestoredSnapshot(ctx, snapshot, uuid.NewString())
	mustMerge(t, err)
	restoredPath := filepath.Join(t.TempDir(), "memlane.sqlite")
	mustMerge(t, os.WriteFile(restoredPath, prepared, 0600))
	restored, err := Open(restoredPath)
	mustMerge(t, err)
	defer restored.Close()
	var state, library string
	mustMerge(t, restored.DB.QueryRow(`SELECT state,library FROM official_capture_receipts WHERE operation=?`, q.Operation).Scan(&state, &library))
	if state != "dispatching" || library != q.Library {
		t.Fatal("restore lost unknown intent", state, library)
	}
	if _, _, err := restored.ClaimOfficialCapture(ctx, q, false); err == nil {
		t.Fatal("restored library reused old dispatch authority")
	}
}
