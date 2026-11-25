package bootstrap

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// PythonCandidates discovers candidates only. Preserve venv symlinks: resolving
// bin/python to its base binary would change which installed libraries are used.
func PythonCandidates(explicit []string) []string {
	paths := append([]string(nil), explicit...)
	for _, name := range []string{"python3.12", "python3", "python"} {
		if p, err := exec.LookPath(name); err == nil {
			paths = append(paths, p)
		}
	}
	paths = append(paths, "/opt/homebrew/bin/python3.12", "/usr/local/bin/python3.12", "/usr/bin/python3")
	if home, err := os.UserHomeDir(); err == nil {
		for _, pattern := range []string{".pyenv/versions/3.12*/bin/python3", ".local/share/uv/python/cpython-3.12*/bin/python3.12"} {
			matches, _ := filepath.Glob(filepath.Join(home, pattern))
			sort.Strings(matches)
			paths = append(paths, matches...)
		}
	}
	var result []string
	seen := map[string]bool{}
	for _, p := range paths {
		p, err := filepath.Abs(p)
		if err != nil || seen[p] {
			continue
		}
		seen[p] = true
		// Retain an explicitly supplied missing path so UI explains its failure.
		result = append(result, p)
	}
	return result
}

type PythonInfo struct {
	Version    string `json:"version"`
	Machine    string `json:"machine"`
	Executable string `json:"executable"`
	Prefix     string `json:"prefix"`
	BasePrefix string `json:"base_prefix"`
}

var (
	routineProbeTimeout     = 30 * time.Second
	postInstallProbeTimeout = 90 * time.Second
)

type postInstallProbeKey struct{}

func withPostInstallProbe(ctx context.Context) context.Context {
	return context.WithValue(ctx, postInstallProbeKey{}, true)
}

func isPostInstallProbe(ctx context.Context) bool {
	marked, _ := ctx.Value(postInstallProbeKey{}).(bool)
	return marked
}

func probeTimeout(ctx context.Context) time.Duration {
	if isPostInstallProbe(ctx) {
		return postInstallProbeTimeout
	}
	return routineProbeTimeout
}

func probeTimeoutText(ctx context.Context) string {
	timeout := probeTimeout(ctx)
	unit, label := time.Second, "秒"
	if timeout < time.Second {
		unit, label = time.Millisecond, "毫秒"
	}
	return fmt.Sprintf("%g %s", float64(timeout)/float64(unit), label)
}

func isolatedPython(ctx context.Context, executable, code string, args ...string) ([]byte, error) {
	probeCtx, cancel := context.WithTimeout(ctx, probeTimeout(ctx))
	defer cancel()
	home, err := os.MkdirTemp("", "memlane-bootstrap-probe-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(home)
	command := exec.CommandContext(probeCtx, executable, append([]string{"-I", "-B", "-c", code}, args...)...)
	var timedOut atomic.Bool
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error {
		if command.Process == nil {
			return os.ErrProcessDone
		}
		err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		if err == nil && probeCtx.Err() != nil && ctx.Err() == nil {
			timedOut.Store(true)
		}
		return err
	}
	command.WaitDelay = 2 * time.Second
	command.Dir = home
	command.Env = []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin", "HOME=" + home, "TMPDIR=" + home,
		"LANG=en_US.UTF-8", "HF_HOME=" + filepath.Join(home, "hf"), "HF_HUB_OFFLINE=1", "TRANSFORMERS_OFFLINE=1",
		"XDG_CACHE_HOME=" + filepath.Join(home, "cache"), "COGNEE_LOGS_DIR=" + filepath.Join(home, "logs"),
		"LOGS_ROOT_DIRECTORY=" + filepath.Join(home, "logs"), "TELEMETRY_DISABLED=true", "PYTHON_DOTENV_DISABLED=1"}
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	// Probe snippets emit bounded metadata; don't propagate inherited credentials.
	if err := command.Start(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if probeCtx.Err() != nil {
			return nil, context.DeadlineExceeded
		}
		return nil, err
	}
	pgid := command.Process.Pid
	waitDone := make(chan struct{})
	var watchWG sync.WaitGroup
	watchWG.Add(1)
	go func() {
		defer watchWG.Done()
		select {
		case <-waitDone:
		case <-probeCtx.Done():
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
		}
	}()
	err = command.Wait()
	close(waitDone)
	watchWG.Wait()
	// Ensure no descendant holding stdout/stderr survives after the probe ends.
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	probeExpired := timedOut.Load() || probeCtx.Err() != nil
	var exit *exec.ExitError
	if err != nil && errors.As(err, &exit) {
		exit.Stderr = stderr.Bytes()
		// A wrapper can report its child's SIGKILL as exit 137 when the
		// deadline kills the process group. Preserve other deterministic exits
		// even if the deadline elapsed while draining a descendant's pipes.
		code := exit.ExitCode()
		normalExit := code >= 0 && (!probeExpired || code != 128+int(syscall.SIGKILL))
		if ws, ok := exit.ProcessState.Sys().(syscall.WaitStatus); ok {
			if (ws.Exited() && normalExit) || (ws.Signaled() && ws.Signal() != syscall.SIGKILL) {
				return nil, exit
			}
		} else if normalExit {
			return nil, exit
		}
	}
	if timedOut.Load() || (err != nil && probeCtx.Err() != nil) {
		return nil, context.DeadlineExceeded
	}
	if err != nil {
		return nil, err
	}
	return stdout.Bytes(), nil
}

func ProbePython(ctx context.Context, executable string) (Observation, error) {
	o := Observation{Path: executable, Status: Missing}
	info, err := os.Stat(executable)
	if errors.Is(err, os.ErrNotExist) {
		o.Reason = "路径不存在"
		return o, nil
	}
	if err != nil {
		return o, err
	}
	o.Status = Incompatible
	if !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		o.Reason = "不是可执行的 Python"
		return o, nil
	}
	b, err := isolatedPython(ctx, executable, `import json,platform,sys; print(json.dumps(dict(version=platform.python_version(),machine=platform.machine(),executable=sys.executable,prefix=sys.prefix,base_prefix=sys.base_prefix)))`)
	if err != nil {
		if ctx.Err() != nil {
			return o, ctx.Err()
		}
		o.Reason = "无法执行 Python 版本检测"
		if errors.Is(err, context.DeadlineExceeded) {
			o.Reason = "Python 版本检测超时（" + probeTimeoutText(ctx) + "）"
			o.transientTimeout = true
		}
		return o, nil
	}
	var p PythonInfo
	if err := json.Unmarshal(b, &p); err != nil {
		o.Reason = "Python 检测回执无效"
		return o, nil
	}
	o.Version = p.Version
	want := map[string]string{"arm64": "arm64", "amd64": "x86_64"}[runtime.GOARCH]
	if !strings.HasPrefix(p.Version, "3.12.") || p.Machine != want {
		o.Reason = "需要与本机架构一致的 Python 3.12（锁定依赖 ABI）"
		return o, nil
	}
	h := sha256.Sum256(b)
	o.Status, o.Fingerprint = Available, hex.EncodeToString(h[:])
	return o, nil
}

// VerifySource implements the fixed archive contract without importing Cognee
// or requiring Python. A package's version metadata alone is never sufficient.
func VerifySource(archive, source, expectedSHA string, packages []string) error {
	var err error
	source, err = filepath.Abs(source)
	if err != nil {
		return err
	}
	if len(expectedSHA) != 64 || len(packages) == 0 {
		return errors.New("fixed source digest required")
	}
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	if hex.EncodeToString(h.Sum(nil)) != expectedSHA {
		return errors.New("fixed official archive digest mismatch")
	}
	if _, err := f.Seek(0, 0); err != nil {
		return err
	}
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	bundle := tar.NewReader(gz)
	allowed, found := map[string]bool{"pyproject.toml": true}, map[string]bool{}
	for _, p := range packages {
		if p == "" || path.Base(p) != p || p == "." || p == ".." {
			return errors.New("invalid package name")
		}
		allowed[p] = true
	}
	names := map[string]bool{}
	root := ""
	for {
		member, err := bundle.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		// GitHub codeload emits a global PAX metadata record before the tree.
		// It is not a filesystem entry or a second archive root.
		if member.Typeflag == tar.TypeXGlobalHeader {
			continue
		}
		name := strings.TrimSuffix(member.Name, "/")
		if path.IsAbs(name) || path.Clean(name) != name || strings.Contains(name, "\\") || name == ".." || strings.HasPrefix(name, "../") {
			return errors.New("unsafe source archive path")
		}
		parts := strings.Split(name, "/")
		if root == "" {
			root = parts[0]
		}
		if parts[0] != root {
			return errors.New("multiple archive roots")
		}
		if len(parts) < 2 || !allowed[parts[1]] {
			continue
		}
		if member.Typeflag == tar.TypeDir {
			continue
		}
		if member.Typeflag != tar.TypeReg {
			return errors.New("linked source package is not supported")
		}
		relative := strings.Join(parts[1:], "/")
		if names[relative] {
			return errors.New("duplicate archive member")
		}
		names[relative], found[parts[1]] = true, true
		target := filepath.Join(source, filepath.FromSlash(relative))
		// Reject package-tree symlinks while allowing a user-selected parent path.
		for current := target; current != source; current = filepath.Dir(current) {
			info, err := os.Lstat(current)
			if err != nil {
				return err
			}
			if info.Mode()&os.ModeSymlink != 0 {
				return errors.New("linked source package is not supported")
			}
		}
		file, err := os.Open(target)
		if err != nil {
			return err
		}
		actual := sha256.New()
		_, err = io.Copy(actual, file)
		file.Close()
		if err != nil {
			return err
		}
		pinned := sha256.New()
		if _, err := io.Copy(pinned, bundle); err != nil {
			return err
		}
		if !strings.EqualFold(hex.EncodeToString(actual.Sum(nil)), hex.EncodeToString(pinned.Sum(nil))) {
			return fmt.Errorf("source differs from pinned archive: %s", relative)
		}
	}
	for p := range allowed {
		if !found[p] {
			return fmt.Errorf("required source missing: %s", p)
		}
	}
	for _, p := range packages {
		err := filepath.WalkDir(filepath.Join(source, p), func(target string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return errors.New("linked source package is not supported")
			}
			if !entry.IsDir() && strings.HasSuffix(target, ".py") {
				rel, _ := filepath.Rel(source, target)
				if !names[filepath.ToSlash(rel)] {
					return errors.New("unexpected official Python module")
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}
