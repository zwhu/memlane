package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestCustomOwnershipFiles(t *testing.T) {
	store, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg := LockConfig{
		OwnerLockFilename: "owner.lock", OwnerReceiptFilename: "owner.json", UpgradeLockFilename: "upgrade.lock",
	}
	inspection, err := cfg.InspectOwnerReadOnly(store)
	if err != nil || inspection.Locked || inspection.HasReceipt {
		t.Fatalf("empty store inspection = %+v, %v", inspection, err)
	}
	if entries, err := os.ReadDir(store); err != nil || len(entries) != 0 {
		t.Fatalf("read-only probe changed the store: %v, %v", entries, err)
	}
	lock, err := cfg.AcquireOwner(store)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.Release() })
	if other, err := cfg.AcquireOwner(store); !errors.Is(err, ErrOwnerLocked) {
		_ = other.Release()
		t.Fatalf("second owner = %v", err)
	}
	locked, exists, err := ProbeFileLockedReadOnly(filepath.Join(store, cfg.OwnerLockFilename))
	if err != nil || !locked || !exists {
		t.Fatalf("owner lock probe = %v, %v, %v", locked, exists, err)
	}
	upgrade, err := cfg.AcquireUpgradeLock(store)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = upgrade.Release() })
	if other, err := cfg.AcquireUpgradeLock(store); !errors.Is(err, ErrUpgradeLocked) {
		_ = other.Release()
		t.Fatalf("second upgrade = %v", err)
	}

	rec := OwnerRecord{
		PID: 1234, Started: "example start", StoreRoot: store,
		ConfigPath: filepath.Join(store, "config.json"), Metadata: json.RawMessage(`{"revision":2}`),
	}
	if err := lock.WriteRecord(rec); err != nil {
		t.Fatal(err)
	}
	got, raw, err := cfg.ReadOwnerRecord(store)
	if err != nil || got.StoreRoot != rec.StoreRoot || got.ConfigPath != rec.ConfigPath ||
		!bytes.Equal(got.Metadata, rec.Metadata) || got.Children == nil || got.ServiceChildren == nil {
		t.Fatalf("read owner = %+v, %v", got, err)
	}
	fingerprint := OwnerFingerprint(rec)
	got.Started = "  " + got.Started + " "
	if len(fingerprint) != 64 || OwnerFingerprint(got) != fingerprint {
		t.Fatal("fingerprint did not normalize the start identity")
	}
	inspection, err = cfg.InspectOwner(store)
	if err != nil || !inspection.Locked || !inspection.HasReceipt || inspection.ReceiptErr != nil ||
		OwnerFingerprint(inspection.Record) != fingerprint {
		t.Fatalf("locked owner inspection = %+v, %v", inspection, err)
	}
	if err := lock.Release(); err != nil {
		t.Fatal(err)
	}
	if other, err := cfg.AcquireOwner(store); !errors.Is(err, ErrRecoveryRequired) {
		_ = other.Release()
		t.Fatalf("owner with stale receipt = %v", err)
	}
	result, err := RecoverStaleOwner(t.Context(), RecoverOptions{
		StoreRoot: store, ConfigPath: rec.ConfigPath, LockConfig: cfg,
		ProcessTable: func(context.Context) (map[int]ProcessInfo, error) { return nil, nil },
	})
	archive := filepath.Base(result.Archive)
	if err != nil || result.Status != "released" || !strings.HasPrefix(archive, "owner-recovered-") {
		t.Fatalf("recovery = %+v, %v", result, err)
	}
	if archived, err := os.ReadFile(result.Archive); err != nil || !bytes.Equal(archived, raw) {
		t.Fatalf("archive = %q, %v", archived, err)
	}
	entries, err := os.ReadDir(store)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	want := []string{archive, cfg.OwnerLockFilename, cfg.UpgradeLockFilename}
	slices.Sort(want)
	if !slices.Equal(names, want) {
		t.Fatalf("store files = %v, want %v", names, want)
	}
}

func TestLockConfigRejectsNonBaseNames(t *testing.T) {
	for _, cfg := range []LockConfig{
		{OwnerLockFilename: "."},
		{OwnerReceiptFilename: ".."},
		{RecoveredReceiptPrefix: "../archive-"},
		{UpgradeLockFilename: "other/upgrade"},
		{ActivationLockFilename: `other\activation`},
	} {
		store := t.TempDir()
		if lock, err := cfg.AcquireOwner(store); err == nil {
			_ = lock.Release()
			t.Fatalf("accepted invalid configuration %+v", cfg)
		}
		if entries, err := os.ReadDir(store); err != nil || len(entries) != 0 {
			t.Fatalf("validation mutated the store: %v, %v", entries, err)
		}
	}
}

func TestReceiptCodecAndExplicitArchivePrefix(t *testing.T) {
	store := t.TempDir()
	cfg := LockConfig{
		RecoveredReceiptPrefix: "archived-",
		Codec: ReceiptCodec{
			Marshal: func(rec OwnerRecord) ([]byte, error) {
				raw, err := json.Marshal(rec)
				return append([]byte("v1:"), raw...), err
			},
			Unmarshal: func(raw []byte) (OwnerRecord, error) {
				body, ok := bytes.CutPrefix(raw, []byte("v1:"))
				if !ok {
					return OwnerRecord{}, errors.New("receipt version missing")
				}
				return unmarshalOwnerRecord(body)
			},
		},
	}
	lock, err := cfg.AcquireOwner(store)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.Release() })
	rec := OwnerRecord{PID: 4321, StoreRoot: store, ConfigPath: filepath.Join(store, "settings.json")}
	if err := lock.WriteRecord(rec); err != nil {
		t.Fatal(err)
	}
	if err := lock.Release(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadOwnerRecord(store); err == nil {
		t.Fatal("default codec accepted a versioned receipt")
	}
	result, err := RecoverStaleOwner(t.Context(), RecoverOptions{
		StoreRoot: store, ConfigPath: rec.ConfigPath, LockConfig: cfg,
		ProcessTable: func(context.Context) (map[int]ProcessInfo, error) { return nil, nil },
	})
	if err != nil || !strings.HasPrefix(filepath.Base(result.Archive), "archived-") {
		t.Fatalf("recovery with custom codec = %+v, %v", result, err)
	}
}
