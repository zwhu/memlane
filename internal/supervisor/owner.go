package supervisor

import (
	"context"
	"encoding/json"
	"os"
	"time"

	"github.com/zwhu/memlane/kit/daemon"
)

var (
	ErrOwnerLocked              = daemon.ErrOwnerLocked
	ErrRecoveryRequired         = daemon.ErrRecoveryRequired
	ErrRecoveryRefused          = daemon.ErrRecoveryRefused
	ErrOwnerMismatch            = daemon.ErrOwnerMismatch
	ErrOwnerProcessChanged      = daemon.ErrOwnerProcessChanged
	ErrOwnerFingerprintMismatch = daemon.ErrOwnerFingerprintMismatch
	ErrOwnerBusy                = daemon.ErrOwnerBusy
	ErrUpgradeLocked            = daemon.ErrUpgradeLocked
)

const ownerLockFilename = ".service-owner"

var ownerLocks = daemon.LockConfig{
	OwnerLockFilename:      ownerLockFilename,
	OwnerReceiptFilename:   ".memlane-dev-owner.json",
	RecoveredReceiptPrefix: ".memlane-dev-owner-recovered-",
	UpgradeLockFilename:    ".memlane-upgrade.lock",
	ActivationLockFilename: "first-install-activation.lock",
	Codec:                  OwnerCodec(),
}

// OwnerCodec returns the unified lossless receipt codec for .memlane-dev-owner.json.
func OwnerCodec() daemon.ReceiptCodec {
	return daemon.ReceiptCodec{
		Marshal:   marshalOwnerRecord,
		Unmarshal: unmarshalOwnerRecord,
	}
}

// FirstInstallLockConfig returns the canonical LockConfig for first-install stores
// (.memlane-dev-owner.json) backed by the unified OwnerCodec.
func FirstInstallLockConfig() daemon.LockConfig {
	return ownerLocks
}

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

// OwnerMetadata carries product-specific fields across the generic receipt codec.
type OwnerMetadata struct {
	ResourcesDir        string
	ResourcesHash       string
	RuntimeConfig       string
	GenerationMode      string
	GenerationReference *GenerationReference
}

// Encode serializes product-specific owner metadata for daemon.Config.OwnerMetadata.
func (meta OwnerMetadata) Encode() json.RawMessage {
	// All fields have fixed JSON-compatible types.
	raw, _ := json.Marshal(meta)
	return raw
}

// DecodeOwnerMetadata decodes product-specific owner metadata from daemon.OwnerRecord.Metadata.
func DecodeOwnerMetadata(raw json.RawMessage) (OwnerMetadata, error) {
	var meta OwnerMetadata
	if len(raw) == 0 {
		return meta, nil
	}
	err := json.Unmarshal(raw, &meta)
	return meta, err
}

func (rec OwnerRecord) daemonRecord() daemon.OwnerRecord {
	return daemon.OwnerRecord{
		PID:                 rec.PID,
		Started:             rec.Started,
		Phase:               rec.Phase,
		ConfigPath:          rec.Config,
		StoreRoot:           rec.OfficialData,
		Endpoints:           []string{rec.ServiceURL, rec.CompletionURL, rec.ProjectControlURL},
		Children:            rec.Children,
		ServiceChildren:     rec.ServiceChildren,
		ChildStartTimes:     rec.ChildStartTimes,
		ObservedDescendants: rec.ObservedDescendants,
		Metadata: OwnerMetadata{
			ResourcesDir:        rec.ResourcesDir,
			ResourcesHash:       rec.ResourcesHash,
			RuntimeConfig:       rec.RuntimeConfig,
			GenerationMode:      rec.GenerationMode,
			GenerationReference: rec.GenerationReference,
		}.Encode(),
	}
}

func ownerRecordFromDaemon(rec daemon.OwnerRecord) (OwnerRecord, error) {
	meta, err := DecodeOwnerMetadata(rec.Metadata)
	if err != nil {
		return OwnerRecord{}, err
	}
	var endpoints [3]string
	copy(endpoints[:], rec.Endpoints)
	return OwnerRecord{
		PID:                 rec.PID,
		Started:             rec.Started,
		Phase:               rec.Phase,
		Config:              rec.ConfigPath,
		OfficialData:        rec.StoreRoot,
		ServiceURL:          endpoints[0],
		CompletionURL:       endpoints[1],
		ProjectControlURL:   endpoints[2],
		Children:            rec.Children,
		ServiceChildren:     rec.ServiceChildren,
		ChildStartTimes:     rec.ChildStartTimes,
		ObservedDescendants: rec.ObservedDescendants,
		ResourcesDir:        meta.ResourcesDir,
		ResourcesHash:       meta.ResourcesHash,
		RuntimeConfig:       meta.RuntimeConfig,
		GenerationMode:      meta.GenerationMode,
		GenerationReference: meta.GenerationReference,
	}, nil
}

func marshalOwnerRecord(rec daemon.OwnerRecord) ([]byte, error) {
	legacy, err := ownerRecordFromDaemon(rec)
	if err != nil {
		return nil, err
	}
	return json.Marshal(legacy)
}

func unmarshalOwnerRecord(raw []byte) (daemon.OwnerRecord, error) {
	var rec OwnerRecord
	err := json.Unmarshal(raw, &rec)
	return rec.daemonRecord(), err
}

type OwnerLock daemon.OwnerLock

func (l *OwnerLock) StoreRoot() string {
	return (*daemon.OwnerLock)(l).StoreRoot()
}

func (l *OwnerLock) ReceiptPath() string {
	return (*daemon.OwnerLock)(l).ReceiptPath()
}

func (l *OwnerLock) WriteRecord(rec OwnerRecord) error {
	return (*daemon.OwnerLock)(l).WriteRecord(rec.daemonRecord())
}

func (l *OwnerLock) RemoveRecord() error {
	return (*daemon.OwnerLock)(l).RemoveRecord()
}

func (l *OwnerLock) Release() error {
	return (*daemon.OwnerLock)(l).Release()
}

type UpgradeLock = daemon.UpgradeLock
type ProcessInfo = daemon.ProcessInfo
type RecoverResult = daemon.RecoverResult

// OwnerInspection summarizes the current .service-owner flock and receipt state.
type OwnerInspection struct {
	StoreRoot  string
	Locked     bool
	HasReceipt bool
	Record     OwnerRecord
	ReceiptErr error
}

// RecoverOptions configures dead-owner receipt recovery.
type RecoverOptions struct {
	StoreRoot    string
	ConfigPath   string
	CheckURLs    []string
	LockFiles    []string
	ProcessTable func(context.Context) (map[int]ProcessInfo, error)
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

func AcquireOwner(storeRoot string) (*OwnerLock, error) {
	lock, err := ownerLocks.AcquireOwner(storeRoot)
	return (*OwnerLock)(lock), err
}

func AcquireUpgradeLock(storeRoot string) (*UpgradeLock, error) {
	return ownerLocks.AcquireUpgradeLock(storeRoot)
}

func OwnerFingerprint(rec OwnerRecord) string {
	return daemon.OwnerFingerprint(daemon.OwnerRecord{
		PID: rec.PID, Started: rec.Started, ConfigPath: rec.Config, StoreRoot: rec.OfficialData,
	})
}

func flock(file *os.File, operation int) error {
	return daemon.Flock(file, operation)
}

func ReadOwnerRecord(storeRoot string) (OwnerRecord, []byte, error) {
	rec, raw, err := ownerLocks.ReadOwnerRecord(storeRoot)
	legacy, codecErr := ownerRecordFromDaemon(rec)
	if err == nil {
		err = codecErr
	}
	return legacy, raw, err
}

func ownerInspectionFromDaemon(in daemon.OwnerInspection, err error) (OwnerInspection, error) {
	rec, codecErr := ownerRecordFromDaemon(in.Record)
	receiptErr := in.ReceiptErr
	if receiptErr == nil {
		receiptErr = codecErr
	}
	return OwnerInspection{
		StoreRoot: in.StoreRoot, Locked: in.Locked, HasReceipt: in.HasReceipt,
		Record: rec, ReceiptErr: receiptErr,
	}, err
}

func InspectOwner(storeRoot string) (OwnerInspection, error) {
	return ownerInspectionFromDaemon(ownerLocks.InspectOwner(storeRoot))
}

func InspectOwnerReadOnly(storeRoot string) (OwnerInspection, error) {
	return ownerInspectionFromDaemon(ownerLocks.InspectOwnerReadOnly(storeRoot))
}

func ProbeFileLockedReadOnly(path string) (bool, bool, error) {
	return daemon.ProbeFileLockedReadOnly(path)
}

func VerifyOwnerRecord(ctx context.Context, storeRoot, configPath string) (OwnerRecord, error) {
	rec, err := ownerLocks.VerifyOwnerRecord(ctx, storeRoot, configPath)
	legacy, codecErr := ownerRecordFromDaemon(rec)
	if err == nil {
		err = codecErr
	}
	return legacy, err
}

func ProcessStartTime(ctx context.Context, pid int) (string, error) {
	return daemon.ProcessStartTime(ctx, pid)
}

func ProcessStartMatches(ctx context.Context, pid int, expected string) (bool, error) {
	return daemon.ProcessStartMatches(ctx, pid, expected)
}

func ProcessTable(ctx context.Context) (map[int]ProcessInfo, error) {
	return daemon.ProcessTable(ctx)
}

func RecoverStaleOwner(ctx context.Context, opts RecoverOptions) (RecoverResult, error) {
	return daemon.RecoverStaleOwner(ctx, daemon.RecoverOptions{
		LockConfig: ownerLocks, StoreRoot: opts.StoreRoot, ConfigPath: opts.ConfigPath,
		CheckURLs: opts.CheckURLs, LockFiles: opts.LockFiles, ProcessTable: opts.ProcessTable,
	})
}

func StopVerifiedOwner(ctx context.Context, storeRoot, configPath string, grace, killWait time.Duration) error {
	return ownerLocks.StopVerifiedOwner(ctx, storeRoot, configPath, grace, killWait)
}

func StopVerifiedOwnerBound(ctx context.Context, opts StopOwnerOptions, expectedPID int, expectedStarted string) error {
	return daemon.StopVerifiedOwnerBound(ctx, daemon.StopOwnerOptions{
		LockConfig: ownerLocks, StoreRoot: opts.StoreRoot, ConfigPath: opts.ConfigPath,
		Grace: opts.Grace, KillWait: opts.KillWait, ExpectedFingerprint: opts.ExpectedFingerprint,
		AllowStarting: opts.AllowStarting, ActivationLockPath: opts.ActivationLockPath,
	}, expectedPID, expectedStarted)
}
