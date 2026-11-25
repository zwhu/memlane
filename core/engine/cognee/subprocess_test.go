package cognee

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zwhu/memlane/core/engine"
	"github.com/zwhu/memlane/kit/daemon"
)

func pythonSubprocessRunner(t *testing.T, module, program string) SubprocessRunner {
	t.Helper()
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is required for subprocess tests")
	}
	dir := t.TempDir()
	pkg := filepath.Join(dir, "engines", "cognee")
	if err := os.MkdirAll(pkg, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkg, module+".py"), []byte(program), 0o600); err != nil {
		t.Fatal(err)
	}
	return SubprocessRunner{PythonBin: python, WorkDir: dir}
}

func TestSubprocessRequiresPython(t *testing.T) {
	for _, bin := range []string{"", " \t\n"} {
		runner := SubprocessRunner{PythonBin: bin}
		err := runner.CheckFirstInstallResources(t.Context(), time.Second)
		if !errors.Is(err, engine.ErrInvalidArgument) {
			t.Errorf("PythonBin %q: error = %v, want invalid argument", bin, err)
		}
	}
}

func TestSubprocessProbeIsolationAndProtocol(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "ambient-openai")
	t.Setenv("LLM_API_KEY", "ambient-llm")
	t.Setenv("PYTHONPATH", t.TempDir())
	ambientHome := t.TempDir()
	t.Setenv("HOME", ambientHome)
	probeTemp := t.TempDir()
	runner := pythonSubprocessRunner(t, "probe", `
import json
import os
from pathlib import Path
import stat
import sys

environment = os.environ
assert 'OPENAI_API_KEY' not in environment and 'LLM_API_KEY' not in environment
assert environment['PYTHONNOUSERSITE'] == '1'
assert environment['PYTHON_DOTENV_DISABLED'] == '1'
assert environment['PYTHONDONTWRITEBYTECODE'] == '1'
assert Path(environment['PYTHONPATH']).resolve() == Path.cwd()
home = Path(environment['HOME'])
assert home == Path(environment['TMPDIR'])
assert stat.S_IMODE(home.stat().st_mode) == 0o700
(home / 'probe-artifact').write_text('sample')
assert os.getpid() == os.getsid(0) == os.getpgrp()
request = json.load(sys.stdin)
assert request == {
    'runtime': {'official_source': 'sample-source', 'generation_mode': 'agent_bridge',
                'generation_max_concurrent_requests': 3, 'generation_backend_kind': 'codex'},
    'environment': {'LLM_API_KEY': 'explicit-key'},
}
sys.stderr.write('log' * 100_000)
print(json.dumps({'status': 'passed', 'plain_passed': True, 'schema_passed': True,
                  'quality_passed': True, 'structured_mode': 'json_schema',
                  'adapter_calls': 2, 'sample_version': 'fixture',
                  'native_schema_evidence': {'status': 'verified'},
                  'boundary': 'official_adapter_synthetic_extraction',
                  'unrecognized_field': 'private'}))
`)
	t.Setenv("TMPDIR", probeTemp)
	runtime := map[string]any{"official_source": "sample-source", "generation_mode": "old-mode"}
	got, err := runner.ProbeModel(context.Background(), engine.ProbeRequest{
		GenerationMode: "agent_bridge", BackendKind: "codex", MaxConcurrentRequests: 3,
		Runtime: runtime, Environment: map[string]string{"LLM_API_KEY": "explicit-key"},
		Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "passed" || !got.PlainPassed || !got.SchemaPassed || !got.QualityPassed ||
		got.StructuredMode != "json_schema" || got.AdapterCalls != 2 || got.Details["sample_version"] != "fixture" {
		t.Fatalf("unexpected probe result %+v", got)
	}
	evidence, ok := got.Details["native_schema_evidence"].(map[string]any)
	if !ok || evidence["status"] != "verified" {
		t.Fatalf("unexpected diagnostic evidence %#v", evidence)
	}
	if _, ok := got.Details["unrecognized_field"]; ok {
		t.Fatal("unexpected diagnostic field")
	}
	if runtime["generation_mode"] != "old-mode" {
		t.Fatal("probe mutated caller runtime")
	}
	if remaining, err := os.ReadDir(probeTemp); err != nil || len(remaining) != 0 {
		t.Fatalf("probe temporary directory contains %v: %v", remaining, err)
	}
}

func TestSubprocessProbeReturnsModelFailure(t *testing.T) {
	runner := pythonSubprocessRunner(t, "probe", `
import json
import sys

assert json.load(sys.stdin) == {'runtime': {}, 'environment': {}}
print(json.dumps({'status': 'result_unknown', 'structured_mode': 'unknown',
                  'error_code': 'provider_timeout'}))
`)
	got, err := runner.ProbeModel(context.Background(), engine.ProbeRequest{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "result_unknown" || got.ErrorCode != "provider_timeout" || got.StructuredMode != "unknown" {
		t.Fatalf("unexpected probe result %+v", got)
	}
}

func TestSubprocessFirstInstallProtocol(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "ambient-openai")
	t.Setenv("LLM_MODEL", "ambient-model")
	t.Setenv("HOME", t.TempDir())
	runner := pythonSubprocessRunner(t, "first_install_store", `
import json
import os
from pathlib import Path
import sys

assert all(key not in os.environ for key in ('HOME', 'OPENAI_API_KEY', 'LLM_MODEL'))
assert os.environ['PYTHON_DOTENV_DISABLED'] == '1'
if sys.argv[1:] == ['--check-resources']:
    sys.exit()
assert sys.argv[1] == '--config' and len(sys.argv) == 3
spec = Path(sys.argv[2])
assert spec.is_absolute() and spec.read_text() == 'test spec'
print(json.dumps({'schema': 1, 'owner_id': 'owner', 'store_id': 'store',
                  'key_id': 'key', 'source_commit': 'fixed', 'storage_initialized': True}))
`)
	spec := filepath.Join(t.TempDir(), "first install spec.json")
	if err := os.WriteFile(spec, []byte("test spec"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := runner.InitFirstInstallStore(context.Background(), spec, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	want := FirstInstallStoreResult{
		Schema: 1, OwnerID: "owner", StoreID: "store", KeyID: "key", SourceCommit: "fixed", StorageInitialized: true,
	}
	if got != want {
		t.Fatalf("receipt %+v, want %+v", got, want)
	}
	if err := runner.CheckFirstInstallResources(context.Background(), 5*time.Second); err != nil {
		t.Fatal(err)
	}
}

func TestSubprocessProbeCancelsEntireGroup(t *testing.T) {
	runner := pythonSubprocessRunner(t, "probe", `
import json
import os
from pathlib import Path
import signal
import subprocess
import sys

def handle_term(signum, frame):
    pass

signal.signal(signal.SIGTERM, handle_term)
request = json.load(sys.stdin)
child = subprocess.Popen([sys.executable, '-c', 'import time; time.sleep(60)'])
ready = Path(request['runtime']['ready'])
temp = ready.with_suffix('.tmp')
temp.write_text(json.dumps([os.getpid(), os.getsid(0), os.getpgid(child.pid)]))
temp.replace(ready)
child.wait()
`)
	ready := filepath.Join(t.TempDir(), "ready.json")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := runner.ProbeModel(ctx, engine.ProbeRequest{
			Runtime: map[string]any{"ready": ready}, Timeout: 10 * time.Second,
		})
		done <- err
	}()

	pids := waitForSubprocessIDs(t, ready, done)
	if len(pids) != 3 || pids[0] != pids[1] || pids[0] != pids[2] {
		t.Fatalf("leader, session and descendant group IDs = %v", pids)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("probe error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("probe did not finish after cancellation")
	}
	if daemon.ProcessGroupExists(pids[0]) {
		t.Fatalf("process group %d survived cancellation", pids[0])
	}
}

func TestSubprocessProbeTimeoutEscalatesToKill(t *testing.T) {
	runner := pythonSubprocessRunner(t, "probe", `
import json
import os
from pathlib import Path
import signal
import sys
import tempfile
import time

signal.signal(signal.SIGTERM, signal.SIG_IGN)
request = json.load(sys.stdin)
scratch = Path(tempfile.mkdtemp())
(scratch / 'python-probe-artifact').touch()
ready = Path(request['runtime']['ready'])
ready.write_text(json.dumps([os.getpid()]))
time.sleep(60)
`)
	ready := filepath.Join(t.TempDir(), "ready.json")
	probeTemp := t.TempDir()
	t.Setenv("TMPDIR", probeTemp)
	done := make(chan error, 1)
	go func() {
		_, err := runner.ProbeModel(context.Background(), engine.ProbeRequest{
			Runtime: map[string]any{"ready": ready}, Timeout: time.Second,
		})
		done <- err
	}()
	pids := waitForSubprocessIDs(t, ready, done)
	if len(pids) != 1 {
		t.Fatalf("unexpected process IDs %v", pids)
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("probe error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("probe did not finish after timeout")
	}
	if daemon.ProcessGroupExists(pids[0]) {
		t.Fatalf("process group %d survived timeout", pids[0])
	}
	if remaining, err := os.ReadDir(probeTemp); err != nil || len(remaining) != 0 {
		t.Fatalf("probe temporary directory contains %v after SIGKILL: %v", remaining, err)
	}
}

func TestSubprocessProbeBoundsOutput(t *testing.T) {
	runner := pythonSubprocessRunner(t, "probe", `
import sys
import time

sys.stdout.write('x' * 65536)
sys.stdout.flush()
sys.stderr.write('private-secret\n')
time.sleep(60)
`)
	_, err := runner.ProbeModel(context.Background(), engine.ProbeRequest{Timeout: 5 * time.Second})
	if err == nil || !strings.Contains(err.Error(), "stdout exceeds size limit") || strings.Contains(err.Error(), "private-secret") {
		t.Fatalf("probe error = %v", err)
	}
}

func waitForSubprocessIDs(t *testing.T, ready string, done <-chan error) []int {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if data, err := os.ReadFile(ready); err == nil {
			var ids []int
			if err := json.Unmarshal(data, &ids); err == nil {
				return ids
			}
		}
		select {
		case err := <-done:
			t.Fatalf("probe exited before recording process IDs: %v", err)
		case <-deadline.C:
			t.Fatal("probe did not record process IDs")
		case <-ticker.C:
		}
	}
}
