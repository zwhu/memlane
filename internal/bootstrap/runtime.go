package bootstrap

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/zwhu/memlane/kit/aiinstaller"
)

// RuntimeDriver never syncs packages into a user's existing interpreter. A
// compatible environment is reused; otherwise consent creates a managed venv
// at its final location, using an already compatible base interpreter.
type RuntimeDriver struct {
	Lock           []byte
	Native         *NativeSupport
	ExistingPython string
	Python         func(context.Context) (string, error)
	UV             func(context.Context) (string, error)
	DownloadSource string
}

func (d RuntimeDriver) Digest() string { h := sha256.Sum256(d.Lock); return hex.EncodeToString(h[:]) }
func (d RuntimeDriver) Probe(ctx context.Context, r Requirement) (Observation, error) {
	if r.ID != "runtime" || r.Digest != d.Digest() {
		return Observation{}, errors.New("runtime requirement differs from fixed lock")
	}
	managed := filepath.Join(r.Destination, "venv/bin/python")
	paths := []string{managed}
	var nativeErr error
	if d.Native != nil {
		nativeErr = d.Native.verify(r.Destination)
		if nativeErr == nil {
			paths = []string{nativeWrapper(r.Destination)}
		} else {
			paths = nil
		}
	}
	if d.ExistingPython != "" && !isPostInstallProbe(ctx) {
		paths = append(paths, d.ExistingPython)
	}
	o := Observation{Status: Missing, Path: managed, Reason: "需要准备引擎运行库"}
	if nativeErr != nil {
		if _, err := os.Stat(managed); err == nil && len(paths) == 0 {
			o.Status = Incompatible
			o.Reason = "需要补齐可独立加载的 OpenSSL 原生依赖"
			var pathErr *os.PathError
			if errors.As(nativeErr, &pathErr) {
				nativeErr = pathErr.Err
			}
			o.Reason += "：" + nativeErr.Error()
		}
	}
	for _, p := range paths {
		candidate, err := ProbeDependencies(ctx, p, d.Lock)
		if err != nil {
			return candidate, err
		}
		if candidate.Status == Available {
			return candidate, nil
		}
		if candidate.Status == Incompatible {
			o = candidate
		}
	}
	return o, nil
}
func (d RuntimeDriver) Install(ctx context.Context, r Requirement) error {
	progressComponent(ctx, r.ID, false)
	if r.ID != "runtime" || r.Digest != d.Digest() {
		return errors.New("runtime requirement differs from fixed lock")
	}
	base, err := d.Python(ctx)
	if err != nil {
		return err
	}
	uv, err := d.UV(ctx)
	if err != nil {
		return err
	}
	identity, _ := json.Marshal(struct{ Lock, Python string }{d.Digest(), base})
	if err := aiinstaller.ClaimComponentRoot(r.Destination, ".memlane-runtime.json", identity); err != nil {
		return err
	}
	lock := filepath.Join(r.Destination, "requirements.lock")
	if b, err := os.ReadFile(lock); err == nil && !bytes.Equal(b, d.Lock) {
		return errors.New("managed requirements changed; retained")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.WriteFile(lock, d.Lock, 0600); err != nil {
		return err
	}
	venv := filepath.Join(r.Destination, "venv")
	python := filepath.Join(venv, "bin/python")
	runUV := func(detail, index string, attempt, total int, args ...string) error {
		stop := progressSync(ctx, detail, index, attempt, total)
		defer stop()
		return runManaged(ctx, r.Destination, uv, args...)
	}
	if _, err := os.Stat(python); errors.Is(err, os.ErrNotExist) {
		if err := runUV("uv venv", "", 0, 0, "--no-config", "--cache-dir", filepath.Join(r.Destination, "cache"), "venv", "--no-project", "--no-python-downloads", "--python", base, venv); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	// uv splits constraint-file arguments on spaces. The process already runs
	// in the owned destination, so keep both lock arguments relative.
	syncArgs := func(indexURL string) []string {
		args := []string{"--no-config", "--cache-dir", filepath.Join(r.Destination, "cache"), "pip", "sync", "--python", python, "--require-hashes", "--build-constraints", filepath.Base(lock)}
		return append(args, "--default-index", indexURL, filepath.Base(lock))
	}
	source := d.DownloadSource
	if source == "" {
		source = DownloadSourceOfficial
	}
	indexes := PyPIIndexURLs(source)
	var syncErr error
	for idx, index := range indexes {
		if err := ctx.Err(); err != nil {
			return err
		}
		progressRuntimeSync(ctx)
		syncErr = runUV("uv pip sync", index, idx+1, len(indexes), syncArgs(index)...)
		if syncErr == nil {
			break
		}
	}
	if syncErr != nil {
		return syncErr
	}
	if d.Native != nil {
		return d.Native.install(ctx, r.Destination)
	}
	return nil
}

type tailWriter struct{ data []byte }

func (w *tailWriter) Write(p []byte) (int, error) {
	n := len(p)
	const limit = 64 * 1024
	if len(p) >= limit {
		w.data = append(w.data[:0], p[len(p)-limit:]...)
	} else {
		w.data = append(w.data, p...)
		if len(w.data) > limit {
			w.data = append([]byte(nil), w.data[len(w.data)-limit:]...)
		}
	}
	return n, nil
}

type CommandFailure = aiinstaller.CommandFailure

func runManaged(ctx context.Context, root, executable string, args ...string) error {
	return runManagedInDir(ctx, root, root, executable, nil, args...)
}

func runManagedInDir(ctx context.Context, root, dir, executable string, env []string, args ...string) error {
	home := filepath.Join(root, "home")
	if err := os.MkdirAll(home, 0700); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.Dir = dir
	cmd.Env = []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin", "HOME=" + home, "TMPDIR=" + root, "LANG=en_US.UTF-8", "UV_NO_CONFIG=1", "UV_PYTHON_DOWNLOADS=never", "PYTHONDONTWRITEBYTECODE=1", "HF_HUB_OFFLINE=1"}
	cmd.Env = append(cmd.Env, env...)
	cmd.Env = append(cmd.Env, proxyEnvVars(ctx)...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	cmd.WaitDelay = 2 * time.Second
	output := &tailWriter{}
	cmd.Stdout, cmd.Stderr = output, output
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return context.Cause(ctx)
		}
		code := -1
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			code = exit.ExitCode()
		} else {
			return &CommandFailure{ExitCode: code, Output: RedactInstallerText(err.Error() + "\n" + string(output.data))}
		}
		return &CommandFailure{ExitCode: code, Output: RedactInstallerText(string(output.data))}
	}
	return nil
}
