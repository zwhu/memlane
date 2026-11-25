package daemon

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// RecoverOptions configures dead-owner receipt recovery.
type RecoverOptions struct {
	LockConfig   LockConfig
	StoreRoot    string
	ConfigPath   string
	CheckURLs    []string
	LockFiles    []string
	ProcessTable func(context.Context) (map[int]ProcessInfo, error)
}

// RecoverResult summarizes stale owner receipt archival.
type RecoverResult struct {
	Status  string `json:"status"`
	Archive string `json:"archive,omitempty"`
}

// RecoverStaleOwner verifies an unowned store receipt belongs only to exited
// processes whose loopback ports are free, then archives it out of the way.
func RecoverStaleOwner(ctx context.Context, opts RecoverOptions) (RecoverResult, error) {
	return recoverStaleOwner(ctx, opts, nil)
}

func recoverStaleOwner(ctx context.Context, opts RecoverOptions, checkReceipt func(OwnerRecord) error) (RecoverResult, error) {
	cfg, err := opts.LockConfig.normalized()
	if err != nil {
		return RecoverResult{}, err
	}
	lock, err := cfg.acquireStoreLock(opts.StoreRoot)
	if err != nil {
		return RecoverResult{}, err
	}
	defer func() {
		_ = lock.Release()
	}()

	if _, statErr := os.Lstat(lock.receiptPath); os.IsNotExist(statErr) {
		return RecoverResult{Status: "already_released"}, nil
	} else if statErr != nil {
		return RecoverResult{}, statErr
	}

	canonicalConfig, err := canonicalPathMaybeMissing(opts.ConfigPath)
	if err != nil {
		return RecoverResult{}, fmt.Errorf("%w: %v", ErrOwnerMismatch, err)
	}

	rec, original, err := cfg.ReadOwnerRecord(lock.StoreRoot())
	if err != nil {
		return RecoverResult{}, fmt.Errorf("%w: %v", ErrRecoveryRefused, err)
	}
	if checkReceipt != nil {
		if err := checkReceipt(rec); err != nil {
			return RecoverResult{}, err
		}
	}

	recConfig, cfgErr := canonicalPathMaybeMissing(rec.ConfigPath)
	recData, dataErr := canonicalDir(rec.StoreRoot)
	if cfgErr != nil || dataErr != nil || recConfig != canonicalConfig || recData != lock.StoreRoot() {
		return RecoverResult{}, fmt.Errorf("%w: receipt targets different store or config", ErrOwnerMismatch)
	}

	pids := map[int]struct{}{rec.PID: {}}
	expectedStarts := make(map[int]string)
	if started := strings.TrimSpace(rec.Started); started != "" {
		expectedStarts[rec.PID] = started
	}
	for _, pid := range rec.Children {
		pids[pid] = struct{}{}
	}
	for _, pid := range rec.ServiceChildren {
		pids[pid] = struct{}{}
	}
	for rawPID, rawStart := range rec.ChildStartTimes {
		pid, parseErr := strconv.Atoi(rawPID)
		if parseErr != nil {
			return RecoverResult{}, fmt.Errorf("%w: invalid child start pid %q", ErrRecoveryRefused, rawPID)
		}
		pids[pid] = struct{}{}
		if started := strings.TrimSpace(rawStart); started != "" {
			expectedStarts[pid] = started
		}
	}
	for rawPID, rawStart := range rec.ObservedDescendants {
		pid, parseErr := strconv.Atoi(rawPID)
		if parseErr != nil {
			return RecoverResult{}, fmt.Errorf("%w: invalid descendant pid %q", ErrRecoveryRefused, rawPID)
		}
		pids[pid] = struct{}{}
		if started := strings.TrimSpace(rawStart); started != "" && expectedStarts[pid] == "" {
			expectedStarts[pid] = started
		}
	}
	for pid := range pids {
		if pid <= 1 {
			return RecoverResult{}, fmt.Errorf("%w: owner process identity unavailable (%d)", ErrRecoveryRefused, pid)
		}
	}

	inspectTable := opts.ProcessTable
	if inspectTable == nil {
		inspectTable = ProcessTable
	}
	table, err := inspectTable(ctx)
	if err != nil {
		return RecoverResult{}, fmt.Errorf("%w: process inspection failed: %v", ErrRecoveryRefused, err)
	}
	isReusedPID := func(pid int, info ProcessInfo) bool {
		expected := expectedStarts[pid]
		curStarted := strings.TrimSpace(info.Started)
		if expected == "" || curStarted == "" {
			return false
		}
		if curStarted == expected {
			return false
		}
		// When inspecting the live process table (not an injected mock table),
		// double-check with ProcessStartMatches to tolerate LC_ALL/locale formatting differences.
		if opts.ProcessTable == nil {
			pCtx, cancel := context.WithTimeout(ctx, time.Second)
			matched, mErr := ProcessStartMatches(pCtx, pid, expected)
			cancel()
			if mErr != nil || matched {
				return false
			}
		}
		return true
	}
	for pid := range pids {
		if info, exists := table[pid]; exists {
			if isReusedPID(pid, info) {
				// POSIX/macOS kernels never recycle pid while any process still has
				// PID/PGID/SID == pid, so a proven lstart mismatch means the original
				// process and its process group have already exited.
				continue
			}
			return RecoverResult{}, fmt.Errorf("%w: recorded process %d remains", ErrRecoveryRefused, pid)
		}
		for _, info := range table {
			if info.PGID == pid {
				return RecoverResult{}, fmt.Errorf("%w: recorded process group %d remains", ErrRecoveryRefused, pid)
			}
		}
	}

	urlSet := make(map[string]struct{})
	for _, raw := range slices.Concat(rec.Endpoints, opts.CheckURLs) {
		trimmed := strings.TrimSpace(raw)
		if trimmed != "" {
			urlSet[trimmed] = struct{}{}
		}
	}
	for raw := range urlSet {
		if err := CheckPortFree(raw); err != nil {
			return RecoverResult{}, fmt.Errorf("%w: endpoint %s unavailable: %v", ErrRecoveryRefused, raw, err)
		}
	}

	var extraLocks []*os.File
	defer func() {
		for _, lf := range extraLocks {
			for {
				if err := syscall.Flock(int(lf.Fd()), syscall.LOCK_UN); err == nil || !errors.Is(err, syscall.EINTR) {
					break
				}
			}
			_ = lf.Close()
		}
	}()
	for _, rawLock := range opts.LockFiles {
		lockFile := strings.TrimSpace(rawLock)
		if lockFile == "" {
			continue
		}
		if !filepath.IsAbs(lockFile) {
			return RecoverResult{}, fmt.Errorf("%w: lock file must be absolute: %s", ErrRecoveryRefused, lockFile)
		}
		if info, statErr := os.Lstat(lockFile); statErr == nil {
			if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
				return RecoverResult{}, fmt.Errorf("%w: lock file must be private regular file: %s", ErrRecoveryRefused, lockFile)
			}
		} else if !os.IsNotExist(statErr) {
			return RecoverResult{}, fmt.Errorf("%w: %v", ErrRecoveryRefused, statErr)
		}
		f, openErr := os.OpenFile(lockFile, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
		if openErr != nil {
			return RecoverResult{}, fmt.Errorf("%w: cannot open lock file %s: %v", ErrRecoveryRefused, lockFile, openErr)
		}
		fInfo, statErr := f.Stat()
		if statErr != nil || !fInfo.Mode().IsRegular() || fInfo.Mode().Perm()&0o077 != 0 {
			_ = f.Close()
			return RecoverResult{}, fmt.Errorf("%w: lock file must be private regular file: %s", ErrRecoveryRefused, lockFile)
		}
		if chmodErr := f.Chmod(0o600); chmodErr != nil {
			_ = f.Close()
			return RecoverResult{}, fmt.Errorf("%w: %v", ErrRecoveryRefused, chmodErr)
		}
		for {
			flockErr := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
			if flockErr == nil {
				break
			}
			if errors.Is(flockErr, syscall.EINTR) {
				continue
			}
			_ = f.Close()
			if errors.Is(flockErr, syscall.EWOULDBLOCK) || errors.Is(flockErr, syscall.EAGAIN) {
				return RecoverResult{}, fmt.Errorf("%w: lock file %s is active", ErrRecoveryRefused, lockFile)
			}
			return RecoverResult{}, fmt.Errorf("%w: lock file %s check failed: %v", ErrRecoveryRefused, lockFile, flockErr)
		}
		extraLocks = append(extraLocks, f)
	}

	_, current, err := cfg.ReadOwnerRecord(lock.StoreRoot())
	if err != nil {
		return RecoverResult{}, fmt.Errorf("%w: %v", ErrRecoveryRefused, err)
	}
	if !bytes.Equal(current, original) {
		return RecoverResult{}, fmt.Errorf("%w: owner receipt changed during recovery", ErrRecoveryRefused)
	}

	archiveName := fmt.Sprintf("%s%d.json", cfg.RecoveredReceiptPrefix, time.Now().UnixNano())
	archivePath := filepath.Join(lock.StoreRoot(), archiveName)
	if err := os.Rename(lock.receiptPath, archivePath); err != nil {
		return RecoverResult{}, err
	}
	return RecoverResult{Status: "released", Archive: archivePath}, nil
}

// collectVerifiedOwnerChildren builds the killable descendant set strictly from
// kernel-start-verified identities (ObservedDescendants and ChildStartTimes) plus
// live direct children whose current PPID equals the verified live ownerPID.
// Bare PIDs from legacy receipts without a verified lstart or live parent link are
// never trusted, preventing PID-reuse kills of unrelated processes.
func collectVerifiedOwnerChildren(
	ctx context.Context,
	rec OwnerRecord,
	ownerAlive bool,
	table map[int]ProcessInfo,
) map[int]string {
	tracked := make(map[int]string)
	if len(table) == 0 {
		return tracked
	}
	selfPID := os.Getpid()
	matchesRecordedStart := func(pid int, expected string) (string, bool) {
		expected = strings.TrimSpace(expected)
		if pid <= 1 || pid == selfPID || expected == "" {
			return "", false
		}
		info, ok := table[pid]
		if !ok || info.Started == "" {
			return "", false
		}
		if info.Started == expected {
			return info.Started, true
		}
		base := ctx
		if base == nil || base.Err() != nil {
			base = context.Background()
		}
		pCtx, cancel := context.WithTimeout(base, time.Second)
		defer cancel()
		matched, err := ProcessStartMatches(pCtx, pid, expected)
		if err == nil && matched {
			return info.Started, true
		}
		return "", false
	}

	// 1. Seed strictly from persisted ObservedDescendants (preserving their recorded identity).
	for rawPID, started := range rec.ObservedDescendants {
		pid, convErr := strconv.Atoi(rawPID)
		if convErr != nil {
			continue
		}
		if canonicalStarted, ok := matchesRecordedStart(pid, started); ok {
			tracked[pid] = canonicalStarted
		}
	}

	// 2. Verify direct children via persisted ChildStartTimes or (only while ownerAlive)
	// strict kernel PPID == rec.PID parentage. Never overwrite an existing tracked entry.
	var verifiedRoots []int
	if ownerAlive && rec.PID > 1 && rec.PID != selfPID {
		verifiedRoots = append(verifiedRoots, rec.PID)
	}
	childCandidates := make(map[int]struct{}, len(rec.Children)+len(rec.ServiceChildren))
	for _, cpid := range rec.Children {
		if cpid > 1 && cpid != selfPID {
			childCandidates[cpid] = struct{}{}
		}
	}
	for _, cpid := range rec.ServiceChildren {
		if cpid > 1 && cpid != selfPID {
			childCandidates[cpid] = struct{}{}
		}
	}
	for cpid := range childCandidates {
		key := strconv.Itoa(cpid)
		if recorded, hasStart := rec.ChildStartTimes[key]; hasStart && strings.TrimSpace(recorded) != "" {
			if canonicalStarted, ok := matchesRecordedStart(cpid, recorded); ok {
				verifiedRoots = append(verifiedRoots, cpid)
				if _, exists := tracked[cpid]; !exists {
					tracked[cpid] = canonicalStarted
				}
			}
			continue
		}
		// Legacy receipt without child_start_times: only accept when the verified owner
		// is still alive and the kernel process table confirms PPID == rec.PID.
		if ownerAlive {
			if info, ok := table[cpid]; ok && info.PPID == rec.PID && info.Started != "" {
				verifiedRoots = append(verifiedRoots, cpid)
				if _, exists := tracked[cpid]; !exists {
					tracked[cpid] = info.Started
				}
			}
		}
	}

	if len(verifiedRoots) > 0 {
		TrackDescendantsFromTable(verifiedRoots, tracked, table)
	}
	delete(tracked, selfPID)
	delete(tracked, rec.PID)
	return tracked
}

// StopVerifiedOwner stops a live external owner after verifying its store,
// configuration path, and kernel start identity.
// When the owner exits or had already died leaving a dirty receipt, verified children are
// stopped and the receipt is reconciled under the exclusive .service-owner flock before returning nil.
func StopVerifiedOwner(ctx context.Context, storeRoot, configPath string, grace, killWait time.Duration) error {
	return DefaultLockConfig().StopVerifiedOwner(ctx, storeRoot, configPath, grace, killWait)
}

func (cfg LockConfig) StopVerifiedOwner(ctx context.Context, storeRoot, configPath string, grace, killWait time.Duration) error {
	if ctx == nil {
		ctx = context.Background()
	}
	insp, err := cfg.InspectOwner(storeRoot)
	if err != nil {
		return err
	}
	if !insp.Locked && !insp.HasReceipt {
		return nil
	}

	// If another launcher just acquired the owner lock and hasn't
	// written the owner receipt yet, wait briefly for the receipt to appear.
	if insp.Locked && !insp.HasReceipt {
		waitDeadline := time.Now().Add(time.Second)
		if d, ok := ctx.Deadline(); ok && d.Before(waitDeadline) {
			waitDeadline = d
		}
		for time.Now().Before(waitDeadline) {
			if ctx.Err() != nil {
				break
			}
			time.Sleep(40 * time.Millisecond)
			if nextInsp, nextErr := cfg.InspectOwner(storeRoot); nextErr == nil {
				insp = nextInsp
				if !insp.Locked || insp.HasReceipt {
					break
				}
			}
		}
		if !insp.Locked && !insp.HasReceipt {
			return nil
		}
	}

	reconcileReceipt := func() error {
		return cfg.reconcileOwnerReceipt(ctx, storeRoot, configPath, nil)
	}

	rec, err := cfg.VerifyOwnerRecord(ctx, storeRoot, configPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if nextInsp, nextErr := cfg.InspectOwner(storeRoot); nextErr == nil && !nextInsp.Locked {
				return nil
			}
		}
		if errors.Is(err, ErrOwnerProcessChanged) {
			nextInsp, nextErr := cfg.InspectOwner(storeRoot)
			if nextErr != nil {
				return nextErr
			}
			if !nextInsp.Locked {
				// Owner already died and released flock, but receipt remained (dirty/unconfirmed stop).
				// Stop any kernel-start-verified children/descendants and reconcile the receipt under lock.
				var errs []error
				probeBase := ctx
				if probeBase.Err() != nil {
					probeBase = context.Background()
				}
				pCtx, pCancel := context.WithTimeout(probeBase, 2*time.Second)
				table, tableErr := ProcessTable(pCtx)
				pCancel()
				if tableErr == nil {
					tracked := collectVerifiedOwnerChildren(ctx, rec, false, table)
					if len(tracked) > 0 {
						effGrace, effKill := partitionStopBudget(ctx, grace, killWait)
						if stopErr := StopTrackedDescendants(ctx, tracked, effGrace, effKill); stopErr != nil {
							errs = append(errs, stopErr)
						}
					}
				}
				if recErr := reconcileReceipt(); recErr != nil {
					errs = append(errs, recErr)
				}
				return errors.Join(errs...)
			}
		}
		return err
	}
	if rec.PID <= 1 || rec.PID == os.Getpid() {
		return nil
	}

	// If the external owner directly supervises this process,
	// send SIGTERM and return immediately so this process can exit without deadlocking
	// the launcher's reverse-order teardown.
	if slices.Contains(rec.Children, os.Getpid()) && os.Getppid() == rec.PID {
		if err := syscall.Kill(rec.PID, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
			return err
		}
		return nil
	}

	return stopLiveOwner(ctx, rec, rec.Started, storeRoot, grace, killWait, cfg.InspectOwner, nil, reconcileReceipt)
}

// StopOwnerOptions configures shutdown of a specific verified owner.
type StopOwnerOptions struct {
	LockConfig          LockConfig
	StoreRoot           string
	ConfigPath          string
	Grace               time.Duration
	KillWait            time.Duration
	ExpectedFingerprint string
	AllowStarting       bool
	ActivationLockPath  string
}

// StopVerifiedOwnerBound stops the owner selected by PID, start time and optional fingerprint.
func StopVerifiedOwnerBound(ctx context.Context, opts StopOwnerOptions, expectedPID int, expectedStarted string) error {
	expectedStarted = strings.TrimSpace(expectedStarted)
	if expectedPID <= 1 || expectedPID == os.Getpid() || expectedStarted == "" {
		return ErrOwnerFingerprintMismatch
	}
	if ctx == nil {
		ctx = context.Background()
	}
	cfg, err := opts.LockConfig.normalized()
	if err != nil {
		return err
	}
	insp, err := cfg.InspectOwnerReadOnly(opts.StoreRoot)
	if err != nil {
		return err
	}
	if !insp.HasReceipt {
		if insp.Locked {
			return ErrOwnerBusy
		}
		return ErrOwnerProcessChanged
	}
	rec, err := cfg.VerifyOwnerRecord(ctx, insp.StoreRoot, opts.ConfigPath)
	if rec.PID != 0 && rec.PID != expectedPID {
		return ErrOwnerFingerprintMismatch
	}
	if rec.PID == expectedPID && strings.TrimSpace(rec.Started) != expectedStarted {
		matched, matchErr := ProcessStartMatches(ctx, expectedPID, expectedStarted)
		if matchErr != nil || !matched {
			return ErrOwnerFingerprintMismatch
		}
	}
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrOwnerProcessChanged
		}
		return err
	}
	checkReceipt := func(current OwnerRecord) error {
		if current.PID != expectedPID || strings.TrimSpace(current.Started) != strings.TrimSpace(rec.Started) ||
			(opts.ExpectedFingerprint != "" && OwnerFingerprint(current) != opts.ExpectedFingerprint) {
			return ErrOwnerFingerprintMismatch
		}
		return nil
	}
	if err := checkReceipt(rec); err != nil {
		return err
	}
	if !opts.AllowStarting && rec.Phase == "starting" {
		return ErrOwnerBusy
	}
	actLock := opts.ActivationLockPath
	if actLock == "" && cfg.ActivationLockFilename != "" {
		actLock = filepath.Join(insp.StoreRoot, cfg.ActivationLockFilename)
	}
	if actLock != "" {
		locked, _, err := ProbeFileLockedReadOnly(actLock)
		if err != nil {
			return err
		}
		if locked {
			return ErrOwnerBusy
		}
	}
	if slices.Contains(rec.Children, os.Getpid()) && os.Getppid() == rec.PID {
		return fmt.Errorf("%w: cannot bound-stop supervising parent", ErrOwnerBusy)
	}

	checkCurrentReceipt := func() error {
		current, _, err := cfg.ReadOwnerRecord(insp.StoreRoot)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		return checkReceipt(current)
	}
	inspectBoundOwner := func(storeRoot string) (OwnerInspection, error) {
		current, err := cfg.InspectOwnerReadOnly(storeRoot)
		if err == nil && current.HasReceipt && current.ReceiptErr == nil {
			err = checkReceipt(current.Record)
		}
		return current, err
	}
	reconcileReceipt := func() error {
		return cfg.reconcileOwnerReceipt(ctx, insp.StoreRoot, opts.ConfigPath, checkReceipt)
	}
	return stopLiveOwner(ctx, rec, expectedStarted, insp.StoreRoot, opts.Grace, opts.KillWait,
		inspectBoundOwner, checkCurrentReceipt, reconcileReceipt)
}

func (cfg LockConfig) reconcileOwnerReceipt(ctx context.Context, storeRoot, configPath string, checkReceipt func(OwnerRecord) error) error {
	if ctx.Err() != nil {
		ctx = context.WithoutCancel(ctx)
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_, err := recoverStaleOwner(ctx, RecoverOptions{LockConfig: cfg, StoreRoot: storeRoot, ConfigPath: configPath}, checkReceipt)
	return err
}

func stopLiveOwner(ctx context.Context, rec OwnerRecord, expectedStarted, storeRoot string, grace, killWait time.Duration,
	inspect func(string) (OwnerInspection, error), checkReceipt, reconcileReceipt func() error,
) error {
	// Snapshot strictly verified child/descendant kernel identities before signaling the owner.
	var tracked map[int]string
	probeBase := ctx
	if probeBase.Err() != nil {
		probeBase = context.Background()
	}
	probeCtx, probeCancel := context.WithTimeout(probeBase, 2*time.Second)
	table, tableErr := ProcessTable(probeCtx)
	probeCancel()
	if tableErr == nil {
		// Trust live parentage only if the bound owner's identity also appears
		// in this snapshot. Persisted descendant identities can stand alone.
		owner := table[rec.PID]
		ownerVerified := checkReceipt == nil || owner.Started == strings.TrimSpace(rec.Started) || owner.Started == expectedStarted
		tracked = collectVerifiedOwnerChildren(ctx, rec, ownerVerified, table)
	}

	effGrace, effKill := partitionStopBudget(ctx, grace, killWait)

	ownerStillMatches := func() bool {
		base := ctx
		if base.Err() != nil {
			base = context.Background()
		}
		pCtx, cancel := context.WithTimeout(base, time.Second)
		defer cancel()
		matched, mErr := ProcessStartMatches(pCtx, rec.PID, expectedStarted)
		return mErr == nil && matched
	}

	// Phase 1: send SIGTERM directly to the verified owner PID so it runs
	// its own reverse-order shutdown and removes the owner receipt cleanly.
	if effGrace > 0 && ctx.Err() == nil {
		alive := ownerStillMatches()
		if checkReceipt != nil {
			if err := checkReceipt(); err != nil {
				return err
			}
		}
		if !alive {
			goto escalate
		}
		if err := syscall.Kill(rec.PID, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
			if !errors.Is(err, syscall.EPERM) || ownerStillMatches() {
				return err
			}
		}
		graceDeadline := time.Now().Add(effGrace)
		for time.Now().Before(graceDeadline) {
			inspNow, inspErr := inspect(storeRoot)
			if errors.Is(inspErr, ErrOwnerFingerprintMismatch) {
				return inspErr
			}
			alive := ownerStillMatches()
			if checkReceipt == nil && !alive && inspErr == nil && !inspNow.Locked && !inspNow.HasReceipt {
				return nil
			}
			if !alive && inspErr == nil && !inspNow.Locked {
				break
			}
			select {
			case <-ctx.Done():
				goto escalate
			case <-time.After(50 * time.Millisecond):
			}
		}
	}

escalate:
	// Phase 2: if the verified owner or any verified children remain alive, escalate to SIGKILL.
	var errs []error
	alive := ownerStillMatches()
	if checkReceipt != nil {
		if err := checkReceipt(); err != nil {
			return err
		}
	}
	if alive {
		if err := syscall.Kill(rec.PID, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
			errs = append(errs, err)
		}
	}
	killWindow := effKill
	if killWindow <= 0 || ctx.Err() != nil {
		killWindow = 500 * time.Millisecond
	}
	if len(tracked) > 0 {
		descCtx, descCancel := context.WithTimeout(context.Background(), killWindow)
		if err := StopTrackedDescendants(descCtx, tracked, killWindow/3, killWindow*2/3); err != nil {
			errs = append(errs, err)
		}
		descCancel()
	}
	killDeadline := time.Now().Add(killWindow)
	for time.Now().Before(killDeadline) {
		inspNow, inspErr := inspect(storeRoot)
		if errors.Is(inspErr, ErrOwnerFingerprintMismatch) {
			return errors.Join(append(errs, inspErr)...)
		}
		if !ownerStillMatches() && inspErr == nil && !inspNow.Locked {
			if !inspNow.HasReceipt {
				return errors.Join(errs...)
			}
			// Reconcile the retained receipt under exclusive flock so we never leave a
			// dirty receipt blocking the next start nor claim success while children/ports live.
			if recErr := reconcileReceipt(); recErr != nil {
				errs = append(errs, recErr)
			}
			return errors.Join(errs...)
		}
		time.Sleep(30 * time.Millisecond)
	}
	if ownerStillMatches() {
		errs = append(errs, fmt.Errorf("external owner process exit unconfirmed: %d", rec.PID))
	} else if inspNow, inspErr := inspect(storeRoot); inspErr == nil {
		if inspNow.Locked {
			errs = append(errs, ErrOwnerLocked)
		} else if inspNow.HasReceipt {
			if recErr := reconcileReceipt(); recErr != nil {
				errs = append(errs, recErr)
			}
		}
	} else if checkReceipt != nil {
		errs = append(errs, inspErr)
	}
	return errors.Join(errs...)
}
