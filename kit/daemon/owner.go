package daemon

import (
	"context"
	"crypto/sha256"
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
	"unicode"
)

var (
	// ErrOwnerLocked indicates an active supervisor holds the canonical .service-owner flock.
	ErrOwnerLocked = errors.New("service owner lock is already held")
	// ErrRecoveryRequired indicates an unrecovered owner receipt exists.
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

const maxOwnerReceiptBytes = 2 << 20

// ReceiptCodec optionally replaces the JSON representation of an owner record.
type ReceiptCodec struct {
	Marshal   func(OwnerRecord) ([]byte, error)
	Unmarshal func([]byte) (OwnerRecord, error)
}

// LockConfig describes the lock and receipt files within a store.
// Empty fields use the defaults. An empty activation lock filename disables its probe.
type LockConfig struct {
	OwnerLockFilename      string
	OwnerReceiptFilename   string
	RecoveredReceiptPrefix string
	UpgradeLockFilename    string
	ActivationLockFilename string
	Codec                  ReceiptCodec
}

// DefaultLockConfig returns the standard filenames and JSON receipt codec.
func DefaultLockConfig() LockConfig {
	return LockConfig{
		OwnerLockFilename:      ".service-owner",
		OwnerReceiptFilename:   ".daemon-owner.json",
		RecoveredReceiptPrefix: ".daemon-owner-recovered-",
		UpgradeLockFilename:    ".daemon-upgrade.lock",
		Codec:                  ReceiptCodec{Marshal: marshalOwnerRecord, Unmarshal: unmarshalOwnerRecord},
	}
}

func marshalOwnerRecord(rec OwnerRecord) ([]byte, error) {
	return json.Marshal(rec)
}

func unmarshalOwnerRecord(raw []byte) (OwnerRecord, error) {
	var rec OwnerRecord
	err := json.Unmarshal(raw, &rec)
	return rec, err
}

func (cfg LockConfig) normalized() (LockConfig, error) {
	defaults := DefaultLockConfig()
	if cfg.OwnerLockFilename == "" {
		cfg.OwnerLockFilename = defaults.OwnerLockFilename
	}
	if cfg.OwnerReceiptFilename == "" {
		cfg.OwnerReceiptFilename = defaults.OwnerReceiptFilename
	}
	if cfg.RecoveredReceiptPrefix == "" {
		cfg.RecoveredReceiptPrefix = strings.TrimSuffix(cfg.OwnerReceiptFilename, ".json") + "-recovered-"
	}
	if cfg.UpgradeLockFilename == "" {
		cfg.UpgradeLockFilename = defaults.UpgradeLockFilename
	}
	for _, name := range []string{
		cfg.OwnerLockFilename, cfg.OwnerReceiptFilename, cfg.RecoveredReceiptPrefix,
		cfg.UpgradeLockFilename, cfg.ActivationLockFilename,
	} {
		if name == "." || name == ".." || strings.ContainsAny(name, "/\\\x00") {
			return LockConfig{}, fmt.Errorf("lock and receipt names must be single base names: %q", name)
		}
	}
	if cfg.Codec.Marshal == nil {
		cfg.Codec.Marshal = defaults.Codec.Marshal
	}
	if cfg.Codec.Unmarshal == nil {
		cfg.Codec.Unmarshal = defaults.Codec.Unmarshal
	}
	return cfg, nil
}

// OwnerRecord describes the process and services that own a store.
type OwnerRecord struct {
	PID                 int               `json:"pid"`
	Started             string            `json:"started"`
	Phase               string            `json:"phase"`
	ConfigPath          string            `json:"config"`
	StoreRoot           string            `json:"store_root"`
	Endpoints           []string          `json:"endpoints,omitempty"`
	Children            []int             `json:"children"`
	ServiceChildren     map[string]int    `json:"service_children"`
	ChildStartTimes     map[string]string `json:"child_start_times,omitempty"`
	ObservedDescendants map[string]string `json:"observed_descendants,omitempty"`
	Metadata            json.RawMessage   `json:"metadata,omitempty"`
}

// OwnerLock holds an exclusive non-blocking advisory lock on <canonical_store>/.service-owner.
type OwnerLock struct {
	mu          sync.Mutex
	file        *os.File
	storeRoot   string
	receiptPath string
	marshal     func(OwnerRecord) ([]byte, error)
	closed      bool
}

// StoreRoot returns the canonicalized store root path guarded by this lock.
func (l *OwnerLock) StoreRoot() string {
	return l.storeRoot
}

// ReceiptPath returns the canonical path to the owner receipt.
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
	store, err := canonicalDir(rec.StoreRoot)
	if err != nil {
		store = filepath.Clean(rec.StoreRoot)
	}
	config, err := canonicalPathMaybeMissing(rec.ConfigPath)
	if err != nil {
		config = filepath.Clean(rec.ConfigPath)
	}
	key := fmt.Sprintf("%d:%q:%q:%q", rec.PID, started, store, config)
	return fmt.Sprintf("%x", sha256.Sum256([]byte(key)))
}

// Flock retries an advisory file lock operation interrupted by a signal.
func Flock(file *os.File, operation int) error {
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
	return DefaultLockConfig().AcquireUpgradeLock(storeRoot)
}

func (cfg LockConfig) AcquireUpgradeLock(storeRoot string) (*UpgradeLock, error) {
	cfg, err := cfg.normalized()
	if err != nil {
		return nil, err
	}
	canonical, err := canonicalDir(storeRoot)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(canonical, cfg.UpgradeLockFilename)
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
		err = Flock(file, syscall.LOCK_EX|syscall.LOCK_NB)
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
	err := errors.Join(Flock(l.file, syscall.LOCK_UN), l.file.Close())
	l.file = nil
	return err
}

func (cfg LockConfig) acquireStoreLock(storeRoot string) (*OwnerLock, error) {
	cfg, err := cfg.normalized()
	if err != nil {
		return nil, err
	}
	canonical, err := canonicalDir(storeRoot)
	if err != nil {
		return nil, err
	}
	lockPath := filepath.Join(canonical, cfg.OwnerLockFilename)
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
		receiptPath: filepath.Join(canonical, cfg.OwnerReceiptFilename),
		marshal:     cfg.Codec.Marshal,
	}, nil
}

// AcquireOwner acquires the exclusive owner lock and refuses an unrecovered receipt.
func AcquireOwner(storeRoot string) (*OwnerLock, error) {
	return DefaultLockConfig().AcquireOwner(storeRoot)
}

func (cfg LockConfig) AcquireOwner(storeRoot string) (*OwnerLock, error) {
	lock, err := cfg.acquireStoreLock(storeRoot)
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
	payload, err := l.marshal(rec)
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

// RemoveRecord deletes the owner receipt, ignoring absence.
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

// ReadOwnerRecord reads and validates a private regular owner receipt.
func ReadOwnerRecord(storeRoot string) (OwnerRecord, []byte, error) {
	return DefaultLockConfig().ReadOwnerRecord(storeRoot)
}

func (cfg LockConfig) ReadOwnerRecord(storeRoot string) (OwnerRecord, []byte, error) {
	cfg, err := cfg.normalized()
	if err != nil {
		return OwnerRecord{}, nil, err
	}
	var rec OwnerRecord
	canonical, err := canonicalDir(storeRoot)
	if err != nil {
		return rec, nil, err
	}
	receiptPath := filepath.Join(canonical, cfg.OwnerReceiptFilename)
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
	rec, err = cfg.Codec.Unmarshal(raw)
	if err != nil {
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
// matching expected in either the current locale or LC_ALL=C (for locale compatibility).
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
// and reads the owner receipt when present without mutating ownership.
func InspectOwner(storeRoot string) (OwnerInspection, error) {
	return DefaultLockConfig().InspectOwner(storeRoot)
}

func (cfg LockConfig) InspectOwner(storeRoot string) (OwnerInspection, error) {
	cfg, err := cfg.normalized()
	if err != nil {
		return OwnerInspection{}, err
	}
	canonical, err := canonicalDir(storeRoot)
	if err != nil {
		return OwnerInspection{}, err
	}
	out := OwnerInspection{StoreRoot: canonical}
	lock, err := cfg.acquireStoreLock(canonical)
	if errors.Is(err, ErrOwnerLocked) {
		out.Locked = true
	} else if err != nil {
		return OwnerInspection{}, err
	} else {
		_ = lock.Release()
	}

	return cfg.inspectOwnerReceipt(out), nil
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
	err = Flock(file, syscall.LOCK_SH|syscall.LOCK_NB)
	if flockBusy(err) {
		return true, true, nil
	}
	if err != nil {
		return false, true, err
	}
	return false, true, Flock(file, syscall.LOCK_UN)
}

// InspectOwnerReadOnly probes the owner lock and reads the receipt without mutating the store.
func InspectOwnerReadOnly(storeRoot string) (OwnerInspection, error) {
	return DefaultLockConfig().InspectOwnerReadOnly(storeRoot)
}

func (cfg LockConfig) InspectOwnerReadOnly(storeRoot string) (OwnerInspection, error) {
	cfg, err := cfg.normalized()
	if err != nil {
		return OwnerInspection{}, err
	}
	canonical, err := canonicalDir(storeRoot)
	if err != nil {
		return OwnerInspection{}, err
	}
	locked, _, err := ProbeFileLockedReadOnly(filepath.Join(canonical, cfg.OwnerLockFilename))
	if err != nil {
		return OwnerInspection{}, err
	}
	return cfg.inspectOwnerReceipt(OwnerInspection{StoreRoot: canonical, Locked: locked}), nil
}

func (cfg LockConfig) inspectOwnerReceipt(out OwnerInspection) OwnerInspection {
	receiptPath := filepath.Join(out.StoreRoot, cfg.OwnerReceiptFilename)
	if _, statErr := os.Lstat(receiptPath); errors.Is(statErr, os.ErrNotExist) {
		return out
	} else if statErr != nil {
		out.HasReceipt = true
		out.ReceiptErr = statErr
		return out
	}
	out.HasReceipt = true
	rec, _, readErr := cfg.ReadOwnerRecord(out.StoreRoot)
	if readErr != nil {
		out.ReceiptErr = readErr
		return out
	}
	out.Record = rec
	return out
}

// VerifyOwnerRecord reads the owner receipt and confirms it matches the target
// store/config and a currently live process with the same kernel start identity.
func VerifyOwnerRecord(ctx context.Context, storeRoot, configPath string) (OwnerRecord, error) {
	return DefaultLockConfig().VerifyOwnerRecord(ctx, storeRoot, configPath)
}

func (cfg LockConfig) VerifyOwnerRecord(ctx context.Context, storeRoot, configPath string) (OwnerRecord, error) {
	rec, _, err := cfg.ReadOwnerRecord(storeRoot)
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
	recConfig, cfgErr := canonicalPathMaybeMissing(rec.ConfigPath)
	recData, dataErr := canonicalDir(rec.StoreRoot)
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
