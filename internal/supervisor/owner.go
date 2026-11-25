package supervisor

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"
)

var (
	// ErrOwnerLocked indicates an active supervisor holds the canonical .service-owner flock.
	ErrOwnerLocked = errors.New("service owner lock is already held")
	// ErrRecoveryRequired indicates an unrecovered .memlane-dev-owner.json receipt exists.
	ErrRecoveryRequired = errors.New("previous ownership record needs confirmed recovery before start")
	// ErrRecoveryRefused indicates stale owner recovery cannot safely release the receipt.
	ErrRecoveryRefused = errors.New("stale owner recovery refused")
	// ErrOwnerMismatch indicates recorded ownership metadata does not match the target runtime.
	ErrOwnerMismatch = errors.New("owner record configuration mismatch")
	// ErrOwnerProcessChanged indicates the recorded owner PID has exited or its kernel start identity changed.
	ErrOwnerProcessChanged      = errors.New("owner process identity changed or exited")
	ErrOwnerFingerprintMismatch = errors.New("owner process fingerprint mismatch")
	ErrOwnerBusy                = errors.New("owner is busy starting or activating")
	ErrUpgradeLocked            = errors.New("store upgrade lock is already held")
)

const (
	ownerLockFilename    = ".service-owner"
	ownerReceiptFilename = ".memlane-dev-owner.json"
	maxOwnerReceiptBytes = 2 << 20
)

// GenerationReference identifies a selected generation profile and its revision.
type GenerationReference struct {
	ID       string `json:"id"`
	Revision int64  `json:"revision"`
}

// OwnerRecord matches the .memlane-dev-owner.json schema expected by dev.py and bootstrap.
type OwnerRecord struct {
	PID                 int                  `json:"pid"`
	Started             string               `json:"started"`
	Phase               string               `json:"phase"`
	Config              string               `json:"config"`
	OfficialData        string               `json:"official_data"`
	ServiceURL          string               `json:"service_url,omitempty"`
	CompletionURL       string               `json:"completion_url,omitempty"`
	ProjectControlURL   string               `json:"project_control_url,omitempty"`
	ResourcesDir        string               `json:"resources_dir,omitempty"`
	ResourcesHash       string               `json:"resources_hash,omitempty"`
	RuntimeConfig       string               `json:"runtime_config,omitempty"`
	GenerationMode      string               `json:"generation_mode,omitempty"`
	GenerationReference *GenerationReference `json:"generation_reference,omitempty"`
	Children            []int                `json:"children"`
	ServiceChildren     map[string]int       `json:"service_children"`
	ChildStartTimes     map[string]string    `json:"child_start_times,omitempty"`
	ObservedDescendants map[string]string    `json:"observed_descendants,omitempty"`
}

// OwnerLock holds an exclusive non-blocking advisory lock on <canonical_store>/.service-owner.
type OwnerLock struct {
	mu          sync.Mutex
	file        *os.File
	storeRoot   string
	receiptPath string
	closed      bool
}

// StoreRoot returns the canonicalized store root path guarded by this lock.
func (l *OwnerLock) StoreRoot() string {
	return l.storeRoot
}

// ReceiptPath returns the canonical path to .memlane-dev-owner.json.
func (l *OwnerLock) ReceiptPath() string {
	return l.receiptPath
}

func canonicalDir(dir string) (string, error) {
	if !filepath.IsAbs(dir) {
		return "", errors.New("store root must be an absolute path")
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(resolved)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("store root must be a directory")
	}
	return abs, nil
}

func canonicalPathMaybeMissing(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", errors.New("path must be absolute")
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return filepath.Abs(resolved)
	} else if !os.IsNotExist(err) {
		return "", err
	}
	parentResolved, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return "", err
	}
	return filepath.Abs(filepath.Join(parentResolved, filepath.Base(path)))
}

// OwnerFingerprint identifies an owner by its PID, start time, store and config.
func OwnerFingerprint(rec OwnerRecord) string {
	started := strings.TrimSpace(rec.Started)
	if rec.PID <= 1 || started == "" {
		return ""
	}
	store, err := canonicalDir(rec.OfficialData)
	if err != nil {
		store = filepath.Clean(rec.OfficialData)
	}
	config, err := canonicalPathMaybeMissing(rec.Config)
	if err != nil {
		config = filepath.Clean(rec.Config)
	}
	key := fmt.Sprintf("%d:%q:%q:%q", rec.PID, started, store, config)
	return fmt.Sprintf("%x", sha256.Sum256([]byte(key)))
}

func flock(file *os.File, operation int) error {
	for {
		err := syscall.Flock(int(file.Fd()), operation)
		if !errors.Is(err, syscall.EINTR) {
			return err
		}
	}
}

func flockBusy(err error) bool {
	return errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN)
}

// UpgradeLock serializes upgrades for the canonical store.
type UpgradeLock struct {
	mu   sync.Mutex
	file *os.File
}

// AcquireUpgradeLock acquires the per-store upgrade lock without waiting.
func AcquireUpgradeLock(storeRoot string) (*UpgradeLock, error) {
	canonical, err := canonicalDir(storeRoot)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(canonical, ".memlane-upgrade.lock")
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() {
			return nil, errors.New("upgrade lock must be a regular file")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err == nil && (!info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0) {
		err = errors.New("upgrade lock must be a private regular file")
	}
	if err == nil {
		err = flock(file, syscall.LOCK_EX|syscall.LOCK_NB)
		if flockBusy(err) {
			err = ErrUpgradeLocked
		}
	}
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	return &UpgradeLock{file: file}, nil
}

// Release unlocks and closes the upgrade lock. It is safe to call more than once.
func (l *UpgradeLock) Release() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return nil
	}
	err := errors.Join(flock(l.file, syscall.LOCK_UN), l.file.Close())
	l.file = nil
	return err
}

func acquireStoreLock(storeRoot string) (*OwnerLock, error) {
	canonical, err := canonicalDir(storeRoot)
	if err != nil {
		return nil, err
	}
	lockPath := filepath.Join(canonical, ownerLockFilename)
	if info, err := os.Lstat(lockPath); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return nil, errors.New("service owner lock must be a regular file")
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	if !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, errors.New("service owner lock must be a regular file")
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return nil, err
	}
	for {
		err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		_ = file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, ErrOwnerLocked
		}
		return nil, err
	}
	return &OwnerLock{
		file:        file,
		storeRoot:   canonical,
		receiptPath: filepath.Join(canonical, ownerReceiptFilename),
	}, nil
}

// AcquireOwner acquires the exclusive .service-owner lock on storeRoot and refuses
// to proceed if an unrecovered .memlane-dev-owner.json receipt already exists.
func AcquireOwner(storeRoot string) (*OwnerLock, error) {
	lock, err := acquireStoreLock(storeRoot)
	if err != nil {
		return nil, err
	}
	if _, statErr := os.Lstat(lock.receiptPath); statErr == nil {
		_ = lock.Release()
		return nil, ErrRecoveryRequired
	} else if !os.IsNotExist(statErr) {
		_ = lock.Release()
		return nil, statErr
	}
	return lock, nil
}

// WriteRecord atomically persists rec with private 0600 permissions.
func (l *OwnerLock) WriteRecord(rec OwnerRecord) error {
	if l == nil {
		return errors.New("nil owner lock")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed || l.file == nil {
		return errors.New("owner lock already released")
	}
	if rec.Children == nil {
		rec.Children = []int{}
	}
	if rec.ServiceChildren == nil {
		rec.ServiceChildren = map[string]int{}
	}
	payload, err := json.Marshal(rec)
	if err != nil {
		return err
	}

	tmpPath := fmt.Sprintf("%s.%d.tmp", l.receiptPath, os.Getpid())
	tmpFile, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return err
	}
	cleanupTmp := true
	defer func() {
		if cleanupTmp {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmpFile.Chmod(0o600); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if _, err := tmpFile.Write(payload); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if err := tmpFile.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, l.receiptPath); err != nil {
		return err
	}
	cleanupTmp = false
	return nil
}

// RemoveRecord deletes .memlane-dev-owner.json, ignoring absence.
func (l *OwnerLock) RemoveRecord() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	receiptPath := l.receiptPath
	closed := l.closed
	l.mu.Unlock()
	if closed || receiptPath == "" {
		return nil
	}
	err := os.Remove(receiptPath)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// Release unlocks and closes the underlying flock file handle idempotently.
func (l *OwnerLock) Release() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil
	}
	l.closed = true
	if l.file == nil {
		return nil
	}
	var errs []error
	for {
		err := syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
		if err == nil {
			break
		}
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		errs = append(errs, err)
		break
	}
	if err := l.file.Close(); err != nil {
		errs = append(errs, err)
	}
	l.file = nil
	return errors.Join(errs...)
}

// ReadOwnerRecord reads and validates a private regular .memlane-dev-owner.json receipt.
func ReadOwnerRecord(storeRoot string) (OwnerRecord, []byte, error) {
	var rec OwnerRecord
	canonical, err := canonicalDir(storeRoot)
	if err != nil {
		return rec, nil, err
	}
	receiptPath := filepath.Join(canonical, ownerReceiptFilename)
	lstat, err := os.Lstat(receiptPath)
	if err != nil {
		return rec, nil, err
	}
	if lstat.Mode()&os.ModeSymlink != 0 || !lstat.Mode().IsRegular() || lstat.Mode().Perm()&0o077 != 0 {
		return rec, nil, errors.New("private regular owner receipt required")
	}

	f, err := os.OpenFile(receiptPath, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return rec, nil, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return rec, nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 || info.Size() > maxOwnerReceiptBytes {
		return rec, nil, errors.New("private regular owner receipt exceeds safety policy")
	}
	raw, err := io.ReadAll(io.LimitReader(f, maxOwnerReceiptBytes+1))
	if err != nil {
		return rec, nil, err
	}
	if len(raw) > maxOwnerReceiptBytes {
		return rec, nil, errors.New("owner receipt exceeds maximum allowed size")
	}
	if err := json.Unmarshal(raw, &rec); err != nil {
		return rec, nil, err
	}
	return rec, raw, nil
}

// ProcessInfo represents one kernel process table entry reported by /bin/ps.
type ProcessInfo struct {
	PID     int
	PPID    int
	PGID    int
	Started string
}

func processStartTimeWithEnv(ctx context.Context, pid int, extraEnv []string) (string, error) {
	if pid < 1 {
		return "", errors.New("process id must be positive")
	}
	cmd := exec.CommandContext(ctx, "/bin/ps", "-p", strconv.Itoa(pid), "-o", "lstart=")
	if len(extraEnv) > 0 {
		cmd.Env = append(os.Environ(), extraEnv...)
	}
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	started := strings.TrimSpace(string(out))
	if started == "" {
		return "", fmt.Errorf("process %d start identity unavailable", pid)
	}
	return started, nil
}

// ProcessStartTime returns the kernel lstart string for pid.
func ProcessStartTime(ctx context.Context, pid int) (string, error) {
	return processStartTimeWithEnv(ctx, pid, nil)
}

// ProcessStartMatches reports whether pid is currently running with kernel lstart
// matching expected in either the current locale or LC_ALL=C (matching scripts/dev.py).
func ProcessStartMatches(ctx context.Context, pid int, expected string) (bool, error) {
	expected = strings.TrimSpace(expected)
	if pid < 1 || expected == "" {
		return false, nil
	}
	actual, err := processStartTimeWithEnv(ctx, pid, nil)
	if err != nil {
		return false, err
	}
	if actual == expected {
		return true, nil
	}
	actualC, errC := processStartTimeWithEnv(ctx, pid, []string{"LC_ALL=C"})
	if errC == nil && actualC == expected {
		return true, nil
	}
	return false, nil
}

// OwnerInspection summarizes the current .service-owner flock and receipt state.
type OwnerInspection struct {
	StoreRoot  string
	Locked     bool
	HasReceipt bool
	Record     OwnerRecord
	ReceiptErr error
}

// InspectOwner performs a non-blocking probe of the canonical .service-owner flock
// and reads .memlane-dev-owner.json when present without mutating ownership.
func InspectOwner(storeRoot string) (OwnerInspection, error) {
	canonical, err := canonicalDir(storeRoot)
	if err != nil {
		return OwnerInspection{}, err
	}
	out := OwnerInspection{StoreRoot: canonical}
	lock, err := acquireStoreLock(canonical)
	if errors.Is(err, ErrOwnerLocked) {
		out.Locked = true
	} else if err != nil {
		return OwnerInspection{}, err
	} else {
		_ = lock.Release()
	}

	return inspectOwnerReceipt(out), nil
}

// ProbeFileLockedReadOnly probes an exclusive flock without creating or changing the file.
func ProbeFileLockedReadOnly(path string) (locked bool, exists bool, err error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	if !info.Mode().IsRegular() {
		return false, true, errors.New("lock must be a regular file")
	}
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if errors.Is(err, os.ErrNotExist) {
		return false, false, nil
	}
	if err != nil {
		return false, true, err
	}
	defer file.Close()
	info, err = file.Stat()
	if err != nil {
		return false, true, err
	}
	if !info.Mode().IsRegular() {
		return false, true, errors.New("lock must be a regular file")
	}
	err = flock(file, syscall.LOCK_SH|syscall.LOCK_NB)
	if flockBusy(err) {
		return true, true, nil
	}
	if err != nil {
		return false, true, err
	}
	return false, true, flock(file, syscall.LOCK_UN)
}

// InspectOwnerReadOnly probes the owner lock and reads the receipt without mutating the store.
func InspectOwnerReadOnly(storeRoot string) (OwnerInspection, error) {
	canonical, err := canonicalDir(storeRoot)
	if err != nil {
		return OwnerInspection{}, err
	}
	locked, _, err := ProbeFileLockedReadOnly(filepath.Join(canonical, ownerLockFilename))
	if err != nil {
		return OwnerInspection{}, err
	}
	return inspectOwnerReceipt(OwnerInspection{StoreRoot: canonical, Locked: locked}), nil
}

func inspectOwnerReceipt(out OwnerInspection) OwnerInspection {

	receiptPath := filepath.Join(out.StoreRoot, ownerReceiptFilename)
	if _, statErr := os.Lstat(receiptPath); errors.Is(statErr, os.ErrNotExist) {
		return out
	} else if statErr != nil {
		out.HasReceipt = true
		out.ReceiptErr = statErr
		return out
	}
	out.HasReceipt = true
	rec, _, readErr := ReadOwnerRecord(out.StoreRoot)
	if readErr != nil {
		out.ReceiptErr = readErr
		return out
	}
	out.Record = rec
	return out
}

// VerifyOwnerRecord reads .memlane-dev-owner.json and confirms it matches the target
// store/config and a currently live process with the same kernel start identity.
func VerifyOwnerRecord(ctx context.Context, storeRoot, configPath string) (OwnerRecord, error) {
	rec, _, err := ReadOwnerRecord(storeRoot)
	if err != nil {
		return OwnerRecord{}, err
	}
	canonicalStore, err := canonicalDir(storeRoot)
	if err != nil {
		return rec, err
	}
	canonicalConfig, err := canonicalPathMaybeMissing(configPath)
	if err != nil {
		return rec, fmt.Errorf("%w: %v", ErrOwnerMismatch, err)
	}
	recConfig, cfgErr := canonicalPathMaybeMissing(rec.Config)
	recData, dataErr := canonicalDir(rec.OfficialData)
	if rec.PID < 1 || cfgErr != nil || dataErr != nil || recConfig != canonicalConfig || recData != canonicalStore {
		return rec, ErrOwnerMismatch
	}
	matched, err := ProcessStartMatches(ctx, rec.PID, rec.Started)
	if err != nil || !matched {
		return rec, ErrOwnerProcessChanged
	}
	return rec, nil
}

func splitFieldsN(s string, maxFields int) []string {
	var fields []string
	i := 0
	n := len(s)
	for len(fields) < maxFields-1 {
		for i < n && unicode.IsSpace(rune(s[i])) {
			i++
		}
		if i >= n {
			return fields
		}
		start := i
		for i < n && !unicode.IsSpace(rune(s[i])) {
			i++
		}
		fields = append(fields, s[start:i])
	}
	for i < n && unicode.IsSpace(rune(s[i])) {
		i++
	}
	if i < n {
		fields = append(fields, strings.TrimSpace(s[i:]))
	}
	return fields
}

// ProcessTable captures the current system process table via /bin/ps.
func ProcessTable(ctx context.Context) (map[int]ProcessInfo, error) {
	out, err := exec.CommandContext(ctx, "/bin/ps", "-axo", "pid=,ppid=,pgid=,lstart=").Output()
	if err != nil {
		return nil, err
	}
	table := make(map[int]ProcessInfo)
	for _, line := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := splitFieldsN(line, 4)
		if len(fields) != 4 {
			continue
		}
		pid, err1 := strconv.Atoi(fields[0])
		ppid, err2 := strconv.Atoi(fields[1])
		pgid, err3 := strconv.Atoi(fields[2])
		if err1 != nil || err2 != nil || err3 != nil || pid <= 0 {
			continue
		}
		table[pid] = ProcessInfo{
			PID:     pid,
			PPID:    ppid,
			PGID:    pgid,
			Started: fields[3],
		}
	}
	return table, nil
}

// RecoverOptions configures dead-owner receipt recovery.
type RecoverOptions struct {
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
	lock, err := acquireStoreLock(opts.StoreRoot)
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

	rec, original, err := ReadOwnerRecord(lock.StoreRoot())
	if err != nil {
		return RecoverResult{}, fmt.Errorf("%w: %v", ErrRecoveryRefused, err)
	}
	if checkReceipt != nil {
		if err := checkReceipt(rec); err != nil {
			return RecoverResult{}, err
		}
	}

	recConfig, cfgErr := canonicalPathMaybeMissing(rec.Config)
	recData, dataErr := canonicalDir(rec.OfficialData)
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
	for _, raw := range append([]string{rec.ServiceURL, rec.CompletionURL, rec.ProjectControlURL}, opts.CheckURLs...) {
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

	_, current, err := ReadOwnerRecord(lock.StoreRoot())
	if err != nil {
		return RecoverResult{}, fmt.Errorf("%w: %v", ErrRecoveryRefused, err)
	}
	if !bytes.Equal(current, original) {
		return RecoverResult{}, fmt.Errorf("%w: owner receipt changed during recovery", ErrRecoveryRefused)
	}

	archiveName := fmt.Sprintf(".memlane-dev-owner-recovered-%d.json", time.Now().UnixNano())
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
		trackDescendantsFromTable(verifiedRoots, tracked, table)
	}
	delete(tracked, selfPID)
	delete(tracked, rec.PID)
	return tracked
}

// StopVerifiedOwner gracefully stops a live external owner (such as scripts/dev.py or a
// previous Memlane session) after strictly verifying .memlane-dev-owner.json matches
// storeRoot, configPath, and kernel lstart identity. Foreign or mismatched owners are refused.
// When the owner exits or had already died leaving a dirty receipt, verified children are
// stopped and the receipt is reconciled under the exclusive .service-owner flock before returning nil.
func StopVerifiedOwner(ctx context.Context, storeRoot, configPath string, grace, killWait time.Duration) error {
	if ctx == nil {
		ctx = context.Background()
	}
	insp, err := InspectOwner(storeRoot)
	if err != nil {
		return err
	}
	if !insp.Locked && !insp.HasReceipt {
		return nil
	}

	// If another launcher just acquired .service-owner milliseconds ago and hasn't
	// written .memlane-dev-owner.json yet, wait briefly for the receipt to appear.
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
			if nextInsp, nextErr := InspectOwner(storeRoot); nextErr == nil {
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
		return reconcileOwnerReceipt(ctx, storeRoot, configPath, nil)
	}

	rec, err := VerifyOwnerRecord(ctx, storeRoot, configPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if nextInsp, nextErr := InspectOwner(storeRoot); nextErr == nil && !nextInsp.Locked {
				return nil
			}
		}
		if errors.Is(err, ErrOwnerProcessChanged) {
			nextInsp, nextErr := InspectOwner(storeRoot)
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

	// If the external owner is a dev launcher that directly supervises this UI process,
	// send SIGTERM and return immediately so this process can exit without deadlocking
	// the launcher's reverse-order UI teardown.
	if slices.Contains(rec.Children, os.Getpid()) && os.Getppid() == rec.PID {
		if err := syscall.Kill(rec.PID, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
			return err
		}
		return nil
	}

	return stopLiveOwner(ctx, rec, rec.Started, storeRoot, grace, killWait, InspectOwner, nil, reconcileReceipt)
}

// StopOwnerOptions configures shutdown of a specific verified owner.
type StopOwnerOptions struct {
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
	insp, err := InspectOwnerReadOnly(opts.StoreRoot)
	if err != nil {
		return err
	}
	if !insp.HasReceipt {
		if insp.Locked {
			return ErrOwnerBusy
		}
		return ErrOwnerProcessChanged
	}
	rec, err := VerifyOwnerRecord(ctx, insp.StoreRoot, opts.ConfigPath)
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
	if actLock == "" {
		actLock = filepath.Join(insp.StoreRoot, "first-install-activation.lock")
	}
	locked, _, err := ProbeFileLockedReadOnly(actLock)
	if err != nil {
		return err
	}
	if locked {
		return ErrOwnerBusy
	}
	if slices.Contains(rec.Children, os.Getpid()) && os.Getppid() == rec.PID {
		return fmt.Errorf("%w: cannot bound-stop supervising parent", ErrOwnerBusy)
	}

	checkCurrentReceipt := func() error {
		current, _, err := ReadOwnerRecord(insp.StoreRoot)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		return checkReceipt(current)
	}
	inspectBoundOwner := func(storeRoot string) (OwnerInspection, error) {
		current, err := InspectOwnerReadOnly(storeRoot)
		if err == nil && current.HasReceipt && current.ReceiptErr == nil {
			err = checkReceipt(current.Record)
		}
		return current, err
	}
	reconcileReceipt := func() error {
		return reconcileOwnerReceipt(ctx, insp.StoreRoot, opts.ConfigPath, checkReceipt)
	}
	return stopLiveOwner(ctx, rec, expectedStarted, insp.StoreRoot, opts.Grace, opts.KillWait,
		inspectBoundOwner, checkCurrentReceipt, reconcileReceipt)
}

func reconcileOwnerReceipt(ctx context.Context, storeRoot, configPath string, checkReceipt func(OwnerRecord) error) error {
	if ctx.Err() != nil {
		ctx = context.WithoutCancel(ctx)
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_, err := recoverStaleOwner(ctx, RecoverOptions{StoreRoot: storeRoot, ConfigPath: configPath}, checkReceipt)
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

	// Phase 1: send SIGTERM directly to the verified owner PID so dev.py / supervisor runs
	// its own reverse-order shutdown and removes .memlane-dev-owner.json cleanly.
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
