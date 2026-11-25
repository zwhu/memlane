package cognee

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/zwhu/memlane/core/engine"
	"github.com/zwhu/memlane/kit/daemon"
)

const (
	probeModule       = "engines.cognee.probe"
	firstStoreModule  = "engines.cognee.first_install_store"
	subprocessMaxOut  = 16 * 1024
	subprocessGrace   = 250 * time.Millisecond
	subprocessKill    = 500 * time.Millisecond
	subprocessPoll    = 10 * time.Millisecond
	subprocessPipeGap = 250 * time.Millisecond
)

// SubprocessRunner invokes the isolated Python engine entry points.
// WorkDir, if set, is the directory that contains the engines package.
type SubprocessRunner struct {
	PythonBin string
	WorkDir   string
}

// FirstInstallStoreResult is the non-secret first-install receipt.
type FirstInstallStoreResult struct {
	Schema             int    `json:"schema"`
	StoreID            string `json:"store_id"`
	OwnerID            string `json:"owner_id"`
	KeyID              string `json:"key_id"`
	SourceCommit       string `json:"source_commit"`
	StorageInitialized bool   `json:"storage_initialized"`
}

func (r SubprocessRunner) ProbeModel(ctx context.Context, req engine.ProbeRequest) (result engine.ProbeResult, err error) {
	runtime := make(map[string]any, len(req.Runtime)+3)
	maps.Copy(runtime, req.Runtime)
	if req.GenerationMode != "" {
		runtime["generation_mode"] = req.GenerationMode
	}
	if req.MaxConcurrentRequests != 0 {
		runtime["generation_max_concurrent_requests"] = req.MaxConcurrentRequests
	}
	if req.BackendKind != "" {
		runtime["generation_backend_kind"] = req.BackendKind
	}
	environment := req.Environment
	if environment == nil {
		environment = map[string]string{}
	}
	input, err := json.Marshal(struct {
		Runtime     map[string]any    `json:"runtime"`
		Environment map[string]string `json:"environment"`
	}{runtime, environment})
	if err != nil {
		return result, errors.New("encode Cognee probe request")
	}
	tempDir, err := filepath.Abs(os.TempDir())
	if err != nil {
		return result, fmt.Errorf("resolve Cognee probe temporary directory: %w", err)
	}
	home, err := os.MkdirTemp(tempDir, "memlane-cognee-probe-")
	if err != nil {
		return result, fmt.Errorf("create Cognee probe home: %w", err)
	}
	defer func() {
		if cleanupErr := os.RemoveAll(home); cleanupErr != nil {
			err = errors.Join(err, fmt.Errorf("remove Cognee probe home: %w", cleanupErr))
		}
	}()

	data, err := r.run(ctx, req.Timeout, home, input, probeModule)
	if err != nil {
		return result, err
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return engine.ProbeResult{}, errors.New("invalid Cognee probe JSON response")
	}
	switch result.Status {
	case "passed", "failed", "result_unknown":
	default:
		return engine.ProbeResult{}, errors.New("invalid Cognee probe status")
	}
	result.Details = make(map[string]any)
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return engine.ProbeResult{}, errors.New("invalid Cognee probe diagnostics")
	}
	for _, key := range []string{
		"structured_mode_evidence", "sample_version", "boundary", "quality_fields",
		"schema_errors", "schema_response_shapes", "capability_calls", "native_schema_evidence",
	} {
		if value, ok := raw[key]; ok {
			result.Details[key] = value
		}
	}
	return result, nil
}

func (r SubprocessRunner) InitFirstInstallStore(ctx context.Context, specPath string, timeout time.Duration) (FirstInstallStoreResult, error) {
	if specPath == "" {
		return FirstInstallStoreResult{}, errors.New("Cognee first-install specification path is required")
	}
	path, err := filepath.Abs(specPath)
	if err != nil {
		return FirstInstallStoreResult{}, err
	}
	data, err := r.run(ctx, timeout, "", nil, firstStoreModule, "--config", path)
	if err != nil {
		return FirstInstallStoreResult{}, err
	}
	var receipt FirstInstallStoreResult
	if err := json.Unmarshal(data, &receipt); err != nil {
		return FirstInstallStoreResult{}, errors.New("invalid Cognee first-install JSON receipt")
	}
	if receipt.Schema != 1 || !receipt.StorageInitialized || receipt.StoreID == "" ||
		receipt.OwnerID == "" || receipt.KeyID == "" || receipt.SourceCommit == "" {
		return FirstInstallStoreResult{}, errors.New("incomplete Cognee first-install receipt")
	}
	return receipt, nil
}

func (r SubprocessRunner) CheckFirstInstallResources(ctx context.Context, timeout time.Duration) error {
	_, err := r.run(ctx, timeout, "", nil, firstStoreModule, "--check-resources")
	return err
}

func (r SubprocessRunner) run(ctx context.Context, timeout time.Duration, home string, input []byte, module string, args ...string) ([]byte, error) {
	if strings.TrimSpace(r.PythonBin) == "" {
		return nil, invalidArgument("Cognee Python executable is required", nil)
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cmd := exec.Command(r.PythonBin, append([]string{"-m", module}, args...)...)
	cmd.Env = subprocessEnvironment(home)
	if r.WorkDir != "" {
		workDir, err := filepath.Abs(r.WorkDir)
		if err != nil {
			return nil, err
		}
		cmd.Dir = workDir
		cmd.Env = append(cmd.Env, "PYTHONPATH="+workDir)
	}
	if input != nil {
		cmd.Stdin = bytes.NewReader(input)
	}
	stdout := &subprocessOutput{exceeded: make(chan struct{})}
	cmd.Stdout = stdout
	// Provider and SDK errors can include credentials. Keep stderr on the null device.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.WaitDelay = subprocessPipeGap
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", module, err)
	}
	wait := make(chan error, 1)
	go func() {
		wait <- cmd.Wait()
	}()

	var waitErr error
	waited := false
	select {
	case waitErr = <-wait:
		waited = true
	case <-ctx.Done():
	case <-stdout.exceeded:
	}
	groupErr := stopSubprocessGroup(cmd.Process.Pid)
	if !waited && groupErr == nil {
		<-wait
	}
	if ctx.Err() != nil {
		return nil, fmt.Errorf("run %s: %w", module, errors.Join(ctx.Err(), groupErr))
	}
	select {
	case <-stdout.exceeded:
		return nil, fmt.Errorf("run %s: %w", module, errors.Join(errors.New("subprocess stdout exceeds size limit"), groupErr))
	default:
	}
	if err := errors.Join(waitErr, groupErr); err != nil {
		return nil, fmt.Errorf("run %s: %w", module, err)
	}
	return stdout.data.Bytes(), nil
}

func subprocessEnvironment(home string) []string {
	env := []string{"PYTHONNOUSERSITE=1", "PYTHON_DOTENV_DISABLED=1", "PYTHONDONTWRITEBYTECODE=1"}
	for _, key := range []string{"PATH", "LANG", "LC_ALL"} {
		if value, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+value)
		}
	}
	if home != "" {
		// The Python probe creates its own scratch home below TMPDIR.
		env = append(env, "HOME="+home, "TMPDIR="+home)
	} else if value, ok := os.LookupEnv("TMPDIR"); ok {
		env = append(env, "TMPDIR="+value)
	}
	return env
}

type subprocessOutput struct {
	data     bytes.Buffer
	exceeded chan struct{}
	full     bool
}

func (w *subprocessOutput) Write(p []byte) (int, error) {
	remaining := subprocessMaxOut - w.data.Len()
	w.data.Write(p[:min(len(p), remaining)])
	if len(p) > remaining && !w.full {
		close(w.exceeded)
		w.full = true
	}
	return len(p), nil
}

func stopSubprocessGroup(pid int) error {
	for _, phase := range []struct {
		signal syscall.Signal
		budget time.Duration
	}{{syscall.SIGTERM, subprocessGrace}, {syscall.SIGKILL, subprocessKill}} {
		if !daemon.ProcessGroupExists(pid) {
			return nil
		}
		if err := syscall.Kill(-pid, phase.signal); err != nil && !errors.Is(err, syscall.ESRCH) {
			return fmt.Errorf("signal Cognee subprocess group %d: %w", pid, err)
		}
		deadline := time.Now().Add(phase.budget)
		for daemon.ProcessGroupExists(pid) && time.Now().Before(deadline) {
			time.Sleep(subprocessPoll)
		}
	}
	if daemon.ProcessGroupExists(pid) {
		return fmt.Errorf("Cognee subprocess group %d did not exit", pid)
	}
	return nil
}
