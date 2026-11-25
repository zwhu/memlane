package bootstrap

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/zwhu/memlane/internal/controlplane"
	"github.com/zwhu/memlane/internal/supervisor"
)

type FirstGenerationReference = supervisor.GenerationReference
type FirstActivationRequest struct {
	Schema    int                      `json:"schema"`
	StoreID   string                   `json:"store_id"`
	Operation string                   `json:"operation_id"`
	Reference FirstGenerationReference `json:"reference"`
	Digest    string                   `json:"digest"`
	Revision  int64                    `json:"revision"`
	Status    string                   `json:"status"`
	Phase     string                   `json:"phase,omitempty"`
}

// ErrFirstActivationRejected identifies a matching receipt rejected before any activation write.
var ErrFirstActivationRejected = errors.New("首次启用在写入前被拒绝，请核对最新提炼配置")

func (r FirstActivationRequest) rejectedBeforeMutation() bool {
	return r.Status == "rejected" && r.Phase == "pre_mutation"
}

func ActivateFirstRuntime(ctx context.Context, prepared FirstRuntime, reference FirstGenerationReference, digest string, revision int64) error {
	if reference.ID == "" || reference.Revision < 1 || len(digest) != 64 || revision < 1 {
		return errors.New("请先验证并选择待启用的记忆提炼配置")
	}
	config, err := loadFirstRuntimeConfig(prepared.ConfigPath)
	if err != nil {
		return err
	}
	requestPath := filepath.Join(config.Root, "first-install-activation.json")
	var attempted FirstActivationRequest
	err = (&Installer{Root: filepath.Dir(prepared.Resources)}).withLock(func() error {
		b, err := privateRead(requestPath)
		if err == nil {
			var existing FirstActivationRequest
			if err := json.Unmarshal(b, &existing); err != nil {
				return err
			}
			if existing.Schema != 1 || existing.StoreID != config.First.StoreID {
				return errors.New("首次启用操作归属不同，保留原回执")
			}
			attempted = existing
			if existing.Status == "completed" {
				return nil
			}
			if !existing.rejectedBeforeMutation() {
				if existing.Reference != reference || existing.Digest != digest || existing.Revision != revision || (existing.Status != "approved" && existing.Status != "stopping" && existing.Status != "starting") {
					return errors.New("原启用操作尚待核对，未创建另一项操作")
				}
				return nil
			}
			if existing.Reference == reference && existing.Digest == digest && existing.Revision == revision {
				_ = os.Remove(requestPath)
				return ErrFirstActivationRejected
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		var identifier [16]byte
		if _, err := rand.Read(identifier[:]); err != nil {
			return err
		}
		attempted = FirstActivationRequest{Schema: 1, StoreID: config.First.StoreID, Operation: "first-" + hex.EncodeToString(identifier[:]), Reference: reference, Digest: digest, Revision: revision, Status: "approved"}
		b, err = json.Marshal(attempted)
		if err != nil {
			return err
		}
		return atomicPrivateFile(requestPath, b)
	})
	if err != nil {
		return err
	}
	prepared.ActivationFile = requestPath
	err = StartFirstRuntime(ctx, prepared)
	if err != nil {
		var receipt FirstActivationRequest
		b, readErr := privateRead(requestPath)
		if readErr == nil && json.Unmarshal(b, &receipt) == nil && receipt.rejectedBeforeMutation() &&
			receipt.Schema == attempted.Schema && receipt.StoreID == attempted.StoreID && receipt.Operation == attempted.Operation &&
			receipt.Reference == attempted.Reference && receipt.Digest == attempted.Digest && receipt.Revision == attempted.Revision {
			_ = (&Installer{Root: filepath.Dir(prepared.Resources)}).withLock(func() error {
				curBytes, curErr := privateRead(requestPath)
				if curErr != nil {
					return nil
				}
				var cur FirstActivationRequest
				if json.Unmarshal(curBytes, &cur) == nil && cur.Operation == attempted.Operation && cur.rejectedBeforeMutation() {
					_ = os.Remove(requestPath)
				}
				return nil
			})
			return ErrFirstActivationRejected
		}
	}
	return err
}

// startFirstRuntimeActivation serializes the handoff until its receipt is durable.
func startFirstRuntimeActivation(ctx context.Context, prepared FirstRuntime, config firstRuntimeConfig, opts firstRuntimeOptions, canonicalStore string) (*FirstRuntimeHandle, error) {
	path := prepared.ActivationFile
	if path != filepath.Join(config.Root, "first-install-activation.json") ||
		config.GenerationFile != filepath.Join(config.Root, "generation.json") {
		return nil, errors.New("首次启用回执不属于当前内容库")
	}
	lock, err := os.OpenFile(filepath.Join(config.Root, "first-install-activation.lock"), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0600)
	if err != nil {
		return nil, err
	}
	defer lock.Close()
	info, err := lock.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("首次启用锁必须是私有文件")
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return nil, err
	}

	var request FirstActivationRequest
	raw, err := privateRead(path)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &request); err != nil {
		return nil, err
	}
	if request.Schema != 1 || request.StoreID != config.First.StoreID || request.Operation == "" {
		return nil, errors.New("首次启用回执身份不同")
	}
	prepared.ActivationFile = ""
	if request.Status == "completed" {
		if complete, err := FirstInitializationComplete(prepared.ConfigPath); err != nil || !complete {
			return nil, errors.Join(errors.New("已完成的首次启用缺少有效初始化回执"), err)
		}
		return startFirstRuntimeHandleLocked(ctx, prepared, config, opts, canonicalStore)
	}
	if request.rejectedBeforeMutation() {
		return nil, ErrFirstActivationRejected
	}
	if request.Status != "approved" && request.Status != "stopping" && request.Status != "starting" {
		return nil, errors.New("首次启用回执需要核对")
	}

	check := func() (activationGeneration, error) {
		state, err := checkActivationGeneration(config.GenerationFile, request)
		if err != nil && request.Status != "starting" {
			request.Status, request.Phase = "rejected", "pre_mutation"
			err = errors.Join(ErrFirstActivationRejected, err, writeFirstRuntimeJSON(path, request))
		}
		return state, err
	}
	if _, err := check(); err != nil {
		return nil, err
	}
	owner, err := supervisor.InspectOwnerReadOnly(config.Root)
	if err != nil {
		return nil, err
	}
	if owner.Locked && owner.Record.Phase == "first_install_setup" {
		request.Status = "stopping"
		if err := writeFirstRuntimeJSON(path, request); err != nil {
			return nil, err
		}
		if existing := firstRuntimeHandles.get(canonicalStore); existing != nil && existing.Owned() {
			err = existing.Shutdown(ctx)
		} else {
			err = supervisor.StopVerifiedOwner(ctx, config.Root, prepared.ConfigPath, opts.stopGrace, opts.killWait)
		}
		if err != nil {
			return nil, err
		}
	} else if owner.Locked && (request.Status != "starting" || config.First.Setup) {
		return nil, supervisor.ErrOwnerBusy
	}
	if !owner.Locked || owner.Record.Phase == "first_install_setup" {
		if err := flipFirstRuntimeSetup(config, prepared.ConfigPath, path, &request, check); err != nil {
			return nil, err
		}
	}
	config.First.Setup = false
	handle, err := startFirstRuntimeHandleLocked(ctx, prepared, config, opts, canonicalStore)
	if err != nil {
		return handle, err
	}

	state, err := checkActivationGeneration(config.GenerationFile, request)
	if err != nil {
		return handle, err
	}
	if state.Active == nil {
		if err := applyFirstGeneration(ctx, prepared.ConfigPath, config, request, state); err != nil {
			return handle, err
		}
	}
	complete, err := FirstInitializationComplete(prepared.ConfigPath)
	if err != nil || !complete {
		return handle, errors.Join(errors.New("首次初始化完成回执尚未确认"), err)
	}
	request.Status = "completed"
	return handle, writeFirstRuntimeJSON(path, request)
}

func flipFirstRuntimeSetup(config firstRuntimeConfig, configPath, receiptPath string, request *FirstActivationRequest, check func() (activationGeneration, error)) error {
	lock, err := supervisor.AcquireOwner(config.Root)
	if err != nil {
		return err
	}
	defer lock.Release()
	if _, err := os.Lstat(lock.ReceiptPath()); !errors.Is(err, os.ErrNotExist) {
		return errors.Join(supervisor.ErrRecoveryRequired, err)
	}
	latest, err := loadFirstRuntimeConfig(configPath)
	if err != nil {
		return err
	}
	if latest.Root != config.Root || latest.First.StoreID != request.StoreID || latest.GenerationFile != config.GenerationFile ||
		(!latest.First.Setup && request.Status != "starting") {
		return errors.New("首次运行配置在切换前已变化")
	}
	if _, err := check(); err != nil {
		return err
	}
	parent, err := os.OpenFile(filepath.Dir(configPath), os.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	defer parent.Close()
	if err := syscall.Flock(int(parent.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	raw, err := privateRead(configPath)
	if err != nil {
		return err
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		return err
	}
	latest.First.Setup = false
	doc["first_install"], err = json.Marshal(latest.First)
	if err != nil {
		return err
	}
	request.Status = "starting"
	if err := writeFirstRuntimeJSON(receiptPath, request); err != nil {
		return err
	}
	return writeFirstRuntimeJSON(configPath, doc)
}

type activationGeneration struct {
	Schema   int                       `json:"schema_version"`
	Revision int64                     `json:"revision"`
	Active   *FirstGenerationReference `json:"active"`
	Next     *FirstGenerationReference `json:"next"`
	Profiles map[string][]struct {
		Revision int64  `json:"revision"`
		Digest   string `json:"digest"`
		Profile  struct {
			Mode  string `json:"mode"`
			Model string `json:"model"`
		} `json:"profile"`
	} `json:"profiles"`
	Validations map[string]struct {
		Digest   string `json:"digest"`
		Revision int64  `json:"registered_revision"`
		Status   string `json:"status"`
		Plain    bool   `json:"plain_passed"`
		Schema   bool   `json:"schema_passed"`
		Quality  bool   `json:"quality_passed"`
	} `json:"validations"`
}

func checkActivationGeneration(path string, request FirstActivationRequest) (activationGeneration, error) {
	var state activationGeneration
	raw, err := privateRead(path)
	if err != nil {
		return state, err
	}
	if err := json.Unmarshal(raw, &state); err != nil {
		return state, err
	}
	mismatch := errors.New("首次启用的提炼选择或验证已变化")
	applied := request.Status == "starting" && state.Active != nil && *state.Active == request.Reference
	if state.Schema != 2 || (!applied && (state.Active != nil || state.Next == nil || *state.Next != request.Reference || state.Revision != request.Revision)) {
		return state, mismatch
	}
	found := false
	for _, row := range state.Profiles[request.Reference.ID] {
		if row.Revision == request.Reference.Revision && row.Digest == request.Digest &&
			(row.Profile.Mode == "provider" || row.Profile.Mode == "agent_bridge") && row.Profile.Model != "" {
			found = true
			break
		}
	}
	var revision int64 = -1
	passed := false
	for _, v := range state.Validations {
		if v.Status == "queued" || v.Status == "running" {
			return state, mismatch
		}
		if v.Digest != request.Digest || v.Revision < revision {
			continue
		}
		ok := v.Status == "passed" && v.Plain && v.Schema && v.Quality
		if v.Revision > revision {
			revision, passed = v.Revision, ok
		} else {
			passed = passed && ok
		}
	}
	if !found || !passed {
		return state, mismatch
	}
	return state, nil
}

func applyFirstGeneration(ctx context.Context, configPath string, config firstRuntimeConfig, request FirstActivationRequest, state activationGeneration) error {
	client := controlplane.NewClient(controlplane.Config{URL: config.Control.URL, ManagementTokenFile: config.Control.TokenFile})
	var current struct {
		State struct {
			ID       string `json:"active_profile_id"`
			Revision int64  `json:"active_revision"`
		} `json:"state"`
		Profiles []struct {
			ID       string `json:"profile_id"`
			Revision int64  `json:"revision"`
			Digest   string `json:"digest"`
			Removed  bool   `json:"removed"`
		} `json:"profiles"`
	}
	if err := client.Request(ctx, "GET", "/api/generation", nil, &current); err != nil {
		return err
	}
	profileFound := false
	for _, profile := range current.Profiles {
		if profile.ID == request.Reference.ID && profile.Revision == request.Reference.Revision && profile.Digest == request.Digest && !profile.Removed {
			profileFound = true
			break
		}
	}
	if !profileFound || (current.State.ID != "" && (current.State.ID != request.Reference.ID || current.State.Revision != request.Reference.Revision)) {
		return errors.New("管理服务的提炼配置与已批准版本不同")
	}
	if current.State.ID == "" {
		var receipt struct {
			Applied bool `json:"active_committed"`
		}
		selection := map[string]any{"profile_id": request.Reference.ID, "revision": request.Reference.Revision}
		if err := client.Request(ctx, "POST", "/api/generation/activate", selection, &receipt); err != nil {
			return err
		}
		if !receipt.Applied {
			return errors.New("引擎未确认首次提炼配置")
		}
	}

	for _, row := range state.Profiles[request.Reference.ID] {
		if row.Revision == request.Reference.Revision {
			if err := updateActivationJSON(configPath, map[string]any{"generation_mode": row.Profile.Mode, "model": row.Profile.Model}); err != nil {
				return err
			}
			break
		}
	}
	now := time.Now()
	clientPath := filepath.Join(config.Root, "client-state.json")
	if err := updateActivationJSON(clientPath, map[string]any{
		"schema_version": 1, "initialization_completed_at": now.Format(time.RFC3339Nano),
		"first_used_date": now.Format("2006-01-02"), "first_used_timezone": now.Location().String(),
	}); err != nil {
		return err
	}
	return updateActivationJSON(config.GenerationFile, map[string]any{"active": request.Reference})
}

func updateActivationJSON(path string, fields map[string]any) error {
	doc := map[string]json.RawMessage{}
	raw, err := privateRead(path)
	if err == nil {
		if err := json.Unmarshal(raw, &doc); err != nil {
			return err
		}
		if doc == nil {
			return errors.New("首次启用状态文件必须为 JSON 对象")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for key, value := range fields {
		doc[key], err = json.Marshal(value)
		if err != nil {
			return err
		}
	}
	return writeFirstRuntimeJSON(path, doc)
}
