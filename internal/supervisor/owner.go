package supervisor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
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
	ErrOwnerProcessChanged = errors.New("owner process identity changed or exited")
)

const (
	ownerLockFilename    = ".service-owner"
	ownerReceiptFilename = ".memlane-dev-owner.json"
	maxOwnerReceiptBytes = 2 << 20
)

// OwnerRecord matches the .memlane-dev-owner.json schema expected by dev.py and bootstrap.
type OwnerRecord struct {
	PID                 int               `json:"pid"`
	Started             string            `json:"started"`
	Phase               string            `json:"phase"`
	Config              string            `json:"config"`
	OfficialData        string            `json:"official_data"`
	ServiceURL          string            `json:"service_url,omitempty"`
	CompletionURL       string            `json:"completion_url,omitempty"`
	ProjectControlURL   string            `json:"project_control_url,omitempty"`
	RuntimeConfig       string            `json:"runtime_config,omitempty"`
	GenerationMode      string            `json:"generation_mode,omitempty"`
	GenerationReference string            `json:"generation_reference,omitempty"`
	Children            []int             `json:"children"`
	ServiceChildren     map[string]int    `json:"service_children"`
	ObservedDescendants map[string]string `json:"observed_descendants,omitempty"`
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

	receiptPath := filepath.Join(canonical, ownerReceiptFilename)
	if _, statErr := os.Lstat(receiptPath); errors.Is(statErr, os.ErrNotExist) {
		return out, nil
	} else if statErr != nil {
		out.HasReceipt = true
		out.ReceiptErr = statErr
		return out, nil
	}
	out.HasReceipt = true
	rec, _, readErr := ReadOwnerRecord(canonical)
	if readErr != nil {
		out.ReceiptErr = readErr
		return out, nil
	}
	out.Record = rec
	return out, nil
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
	recConfig, cfgErr := canonicalPathMaybeMissing(rec.Config)
	recData, dataErr := canonicalDir(rec.OfficialData)
	if cfgErr != nil || dataErr != nil || recConfig != canonicalConfig || recData != lock.StoreRoot() {
		return RecoverResult{}, fmt.Errorf("%w: receipt targets different store or config", ErrOwnerMismatch)
	}

	pids := map[int]struct{}{rec.PID: {}}
	for _, pid := range rec.Children {
		pids[pid] = struct{}{}
	}
	for _, pid := range rec.ServiceChildren {
		pids[pid] = struct{}{}
	}
	for rawPID := range rec.ObservedDescendants {
		pid, parseErr := strconv.Atoi(rawPID)
		if parseErr != nil {
			return RecoverResult{}, fmt.Errorf("%w: invalid descendant pid %q", ErrRecoveryRefused, rawPID)
		}
		pids[pid] = struct{}{}
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
	for pid := range pids {
		if _, exists := table[pid]; exists {
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
