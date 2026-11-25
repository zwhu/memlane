package supervisor

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestInspectOwnerReadOnlyCreatesNoFiles(t *testing.T) {
	store := t.TempDir()
	before, err := os.Stat(store)
	if err != nil {
		t.Fatal(err)
	}
	insp, err := InspectOwnerReadOnly(store)
	if err != nil || insp.Locked || insp.HasReceipt {
		t.Fatalf("empty store inspection = %+v, %v", insp, err)
	}
	entries, err := os.ReadDir(store)
	if err != nil || len(entries) != 0 {
		t.Fatalf("empty store entries = %v, %v", entries, err)
	}
	after, err := os.Stat(store)
	if err != nil || !before.ModTime().Equal(after.ModTime()) {
		t.Fatalf("store directory modified: %v, %v", after, err)
	}

	lock, err := AcquireOwner(store)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.Release() })
	path := filepath.Join(store, ownerLockFilename)
	// A mode other than 0600 detects any attempted permission normalization.
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatal(err)
	}
	before, err = os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, wantLocked := range []bool{true, false} {
		insp, err := InspectOwnerReadOnly(store)
		if err != nil || insp.Locked != wantLocked || insp.HasReceipt {
			t.Fatalf("inspection with locked=%v: %+v, %v", wantLocked, insp, err)
		}
		after, err := os.Stat(path)
		if err != nil || after.Mode() != before.Mode() || !after.ModTime().Equal(before.ModTime()) {
			t.Fatalf("owner lock modified: %v, %v", after, err)
		}
		if err := lock.Release(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAcquireUpgradeLock(t *testing.T) {
	store := t.TempDir()
	alias := filepath.Join(t.TempDir(), "store alias")
	if err := os.Symlink(store, alias); err != nil {
		t.Fatal(err)
	}
	lock, err := AcquireUpgradeLock(store)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.Release() })
	if other, err := AcquireUpgradeLock(alias); !errors.Is(err, ErrUpgradeLocked) {
		_ = other.Release()
		t.Fatalf("overlapping upgrade = %v", err)
	}
	for range 2 {
		if err := lock.Release(); err != nil {
			t.Fatal(err)
		}
	}
	again, err := AcquireUpgradeLock(alias)
	if err != nil {
		t.Fatal(err)
	}
	if err := again.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestStopVerifiedOwnerBound(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	store := t.TempDir()
	opts := StopOwnerOptions{StoreRoot: store, ConfigPath: filepath.Join(store, "runtime.json"),
		Grace: time.Second, KillWait: time.Second}
	owner := exec.Command("/bin/sleep", "60")
	if err := owner.Start(); err != nil {
		t.Fatal(err)
	}
	exitReady := make(chan struct{})
	var exitErr error
	go func() {
		exitErr = owner.Wait()
		close(exitReady)
	}()
	t.Cleanup(func() {
		_ = owner.Process.Kill()
		<-exitReady
	})
	pid := owner.Process.Pid
	started, err := ProcessStartTime(ctx, pid)
	if err != nil {
		t.Fatal(err)
	}

	if err := StopVerifiedOwnerBound(ctx, opts, pid, started); !errors.Is(err, ErrOwnerProcessChanged) {
		t.Fatalf("empty store: %v", err)
	}
	lock, err := AcquireOwner(store)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.Release() })
	if err := StopVerifiedOwnerBound(ctx, opts, pid, started); !errors.Is(err, ErrOwnerBusy) {
		t.Fatalf("locked store without receipt: %v", err)
	}
	rec := OwnerRecord{PID: pid, Started: started, OfficialData: store, Config: opts.ConfigPath, Phase: "starting"}
	if err := lock.WriteRecord(rec); err != nil {
		t.Fatal(err)
	}
	opts.ExpectedFingerprint = OwnerFingerprint(rec)
	if len(opts.ExpectedFingerprint) != 64 {
		t.Fatalf("invalid fingerprint %q", opts.ExpectedFingerprint)
	}
	alias := filepath.Join(t.TempDir(), "store alias")
	if err := os.Symlink(store, alias); err != nil {
		t.Fatal(err)
	}
	aliasRecord := rec
	aliasRecord.Started = " " + rec.Started + " "
	aliasRecord.OfficialData = alias
	aliasRecord.Config = filepath.Join(alias, "runtime.json")
	if OwnerFingerprint(aliasRecord) != opts.ExpectedFingerprint {
		t.Fatal("canonical paths and trimmed start must have the same fingerprint")
	}
	for _, tc := range []struct {
		name        string
		pid         int
		started     string
		fingerprint string
		want        error
	}{
		{"wrong PID", pid + 1, started, opts.ExpectedFingerprint, ErrOwnerFingerprintMismatch},
		{"self PID", os.Getpid(), started, opts.ExpectedFingerprint, ErrOwnerFingerprintMismatch},
		{"empty start", pid, " ", opts.ExpectedFingerprint, ErrOwnerFingerprintMismatch},
		{"wrong start", pid, "another start", opts.ExpectedFingerprint, ErrOwnerFingerprintMismatch},
		{"wrong fingerprint", pid, started, "another fingerprint", ErrOwnerFingerprintMismatch},
		{"starting", pid, started, opts.ExpectedFingerprint, ErrOwnerBusy},
	} {
		t.Run(tc.name, func(t *testing.T) {
			attempt := opts
			attempt.ExpectedFingerprint = tc.fingerprint
			if err := StopVerifiedOwnerBound(ctx, attempt, tc.pid, tc.started); !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
			if matched, err := ProcessStartMatches(ctx, pid, started); err != nil || !matched {
				t.Fatalf("owner changed after rejected stop: %v, %v", matched, err)
			}
		})
	}

	actPath := filepath.Join(store, "first-install-activation.lock")
	if _, err := os.Lstat(actPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rejected stops created an activation lock: %v", err)
	}
	act, err := os.OpenFile(actPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer act.Close()
	if err := flock(act, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	opts.AllowStarting = true
	if err := StopVerifiedOwnerBound(ctx, opts, pid, started); !errors.Is(err, ErrOwnerBusy) {
		t.Fatalf("locked activation: %v", err)
	}
	if err := flock(act, syscall.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	released := make(chan error, 1)
	go func() {
		<-exitReady
		released <- lock.Release()
	}()
	if err := StopVerifiedOwnerBound(ctx, opts, pid, " "+started+" "); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-released:
		if err != nil {
			t.Fatal(err)
		}
		var status *exec.ExitError
		if !errors.As(exitErr, &status) || status.Sys().(syscall.WaitStatus).Signal() != syscall.SIGTERM {
			t.Fatalf("owner did not exit by SIGTERM: %v", exitErr)
		}
	case <-ctx.Done():
		t.Fatal("owner did not exit")
	}
	insp, err := InspectOwnerReadOnly(store)
	if err != nil || insp.Locked || insp.HasReceipt {
		t.Fatalf("after stop: %+v, %v", insp, err)
	}
	archives, err := filepath.Glob(filepath.Join(store, ".memlane-dev-owner-recovered-*.json"))
	if err != nil || len(archives) != 1 {
		t.Fatalf("retained receipt was not archived: %v, %v", archives, err)
	}
}

func TestStopVerifiedOwnerBoundRefusesEscalationAfterReceiptReplacement(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	store := t.TempDir()
	config := filepath.Join(store, "runtime.json")
	ready := filepath.Join(store, "ready")
	replaced := filepath.Join(store, "replaced")

	lock, err := AcquireOwner(store)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	// The owner simulates a receipt replacement during its TERM handler and
	// stays alive so the test can detect an unwanted SIGKILL.
	owner := exec.Command("/bin/sh", "-c", `
		trap 'mv "$2" "$3"' TERM
		touch "$1"
		while :; do sleep 0.02; done
	`, "owner", ready, replaced, lock.ReceiptPath())
	if err := owner.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = owner.Process.Kill()
		_ = owner.Wait()
	}()
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("owner did not become ready")
		case <-time.After(10 * time.Millisecond):
		}
	}
	pid := owner.Process.Pid
	started, err := ProcessStartTime(ctx, pid)
	if err != nil {
		t.Fatal(err)
	}
	rec := OwnerRecord{PID: pid, Started: started, OfficialData: store, Config: config, Phase: "ready"}
	if err := lock.WriteRecord(rec); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(replaced, []byte(`{"pid":2,"started":"different owner"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	opts := StopOwnerOptions{StoreRoot: store, ConfigPath: config, Grace: time.Second, KillWait: time.Second,
		ExpectedFingerprint: OwnerFingerprint(rec)}
	if err := StopVerifiedOwnerBound(ctx, opts, pid, started); !errors.Is(err, ErrOwnerFingerprintMismatch) {
		t.Fatalf("receipt replacement during shutdown: %v", err)
	}
	if matched, err := ProcessStartMatches(ctx, pid, started); err != nil || !matched {
		t.Fatalf("owner changed after refused escalation: %v, %v", matched, err)
	}
	current, _, err := ReadOwnerRecord(store)
	if err != nil || current.PID != 2 {
		t.Fatalf("replacement receipt changed: %+v, %v", current, err)
	}
}
