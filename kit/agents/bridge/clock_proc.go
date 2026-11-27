package bridge

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/zwhu/memlane/kit/daemon"
)

var ErrIdleTimeout = errors.New("process idle timeout")
var errLineLimit = errors.New("process output limit")

type ActivityClock struct {
	mu       sync.Mutex
	timeout  time.Duration
	deadline time.Time
}

func NewActivityClock(timeout time.Duration) *ActivityClock {
	return &ActivityClock{timeout: timeout, deadline: time.Now().Add(timeout)}
}

func (c *ActivityClock) Touch() {
	c.mu.Lock()
	c.deadline = time.Now().Add(c.timeout)
	c.mu.Unlock()
}

func (c *ActivityClock) Remaining() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return time.Until(c.deadline)
}
func (c *ActivityClock) Expired() bool { return c.Remaining() <= 0 }

type idleResult[T any] struct {
	value T
	err   error
}

func withIdle[T any](ctx context.Context, clock *ActivityClock, operation func() (T, error)) (T, error) {
	return withCancelableIdle(ctx, clock, operation, nil)
}

func withCancelableIdle[T any](ctx context.Context, clock *ActivityClock, operation func() (T, error), interrupt func() bool) (T, error) {
	var zero T
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	if clock.Expired() {
		return zero, ErrIdleTimeout
	}
	done := make(chan idleResult[T], 1)
	go func() { v, err := operation(); done <- idleResult[T]{v, err} }()
	timer := time.NewTimer(max(0, clock.Remaining()))
	defer timer.Stop()
	stop := func() {
		if interrupt != nil && interrupt() {
			<-done
		}
	}
	for {
		select {
		case <-ctx.Done():
			stop()
			return zero, ctx.Err()
		case result := <-done:
			if err := ctx.Err(); err != nil {
				return zero, err
			}
			return result.value, result.err
		case <-timer.C:
			if next := clock.Remaining(); next > 0 {
				timer.Reset(next)
			} else {
				stop()
				return zero, ErrIdleTimeout
			}
		}
	}
}

type activityReader struct {
	io.Reader
	clock *ActivityClock
}

func (r activityReader) Read(data []byte) (int, error) {
	n, err := r.Reader.Read(data)
	if n > 0 {
		r.clock.Touch()
	}
	return n, err
}

// ReadLineWithIdle consumes a newline-delimited record. Pass the same buffered
// reader across calls. For chunk-level activity, wrap its underlying reader.
func ReadLineWithIdle(ctx context.Context, reader *bufio.Reader, clock *ActivityClock, maxLineBytes int) ([]byte, error) {
	return withIdle(ctx, clock, func() ([]byte, error) {
		var line []byte
		for {
			fragment, err := reader.ReadSlice('\n')
			if len(fragment) > 0 {
				clock.Touch()
			}
			if len(fragment) > maxLineBytes-len(line) {
				return nil, errLineLimit
			}
			line = append(line, fragment...)
			if errors.Is(err, bufio.ErrBufferFull) {
				continue
			}
			if errors.Is(err, io.EOF) && len(line) > 0 {
				err = nil
			}
			return line, err
		}
	})
}

func WriteAllWithIdle(ctx context.Context, writer io.Writer, data []byte, clock *ActivityClock) error {
	interrupt := func() bool {
		if pipe, ok := writer.(*os.File); ok {
			return pipe.SetWriteDeadline(time.Now()) == nil
		}
		if closer, ok := writer.(io.Closer); ok {
			return closer.Close() == nil
		}
		return false
	}
	_, err := withCancelableIdle(ctx, clock, func() (struct{}, error) {
		for len(data) > 0 {
			n, e := writer.Write(data)
			if e != nil {
				return struct{}{}, e
			}
			if n <= 0 || n > len(data) {
				return struct{}{}, io.ErrShortWrite
			}
			data = data[n:]
		}
		return struct{}{}, nil
	}, interrupt)
	return err
}

// IsolatedProcess owns all three pipes and its session leader. Wait has no
// pipe dependencies because the parent owns the reading ends.
type IsolatedProcess struct {
	PID            int
	Stdin          *os.File
	Stdout         *bufio.Reader
	Clock          *ActivityClock
	stdout, stderr *os.File
	done, drained  chan struct{}
	waitErr        error
	diagnostic     bytes.Buffer
	reapOnce       sync.Once
	reapErr        error
}

func StartIsolatedProcess(ctx context.Context, cmd *exec.Cmd, clock *ActivityClock) (*IsolatedProcess, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	stdin, in, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	stdout, out, err := os.Pipe()
	if err != nil {
		stdin.Close()
		in.Close()
		return nil, err
	}
	stderr, diag, err := os.Pipe()
	if err != nil {
		stdin.Close()
		in.Close()
		stdout.Close()
		out.Close()
		return nil, err
	}
	defer stdin.Close()
	defer out.Close()
	defer diag.Close()
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, out, diag
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		in.Close()
		stdout.Close()
		stderr.Close()
		return nil, err
	}
	clock.Touch()
	p := &IsolatedProcess{PID: cmd.Process.Pid, Stdin: in, Stdout: bufio.NewReader(activityReader{stdout, clock}),
		Clock: clock, stdout: stdout, stderr: stderr, done: make(chan struct{}), drained: make(chan struct{})}
	go func() { p.waitErr = cmd.Wait(); close(p.done) }()
	go func() {
		defer close(p.drained)
		var buf [4096]byte
		reader := activityReader{stderr, clock}
		for {
			n, err := reader.Read(buf[:])
			p.diagnostic.Write(buf[:min(n, max(0, 16384-p.diagnostic.Len()))])
			if err != nil {
				return
			}
		}
	}()
	return p, nil
}

func (p *IsolatedProcess) Reap(ctx context.Context, termGrace, killWait time.Duration) error {
	p.reapOnce.Do(func() {
		defer p.stdout.Close()
		defer p.stderr.Close()
		p.Stdin.Close()
		if !p.stopGroup(ctx, syscall.SIGTERM, termGrace) && !p.stopGroup(ctx, syscall.SIGKILL, killWait) {
			p.reapErr = errors.New("process group cleanup unconfirmed")
			return
		}
		// Once the group is gone, it has closed the remaining pipe writers.
		select {
		case <-p.drained:
		case <-ctx.Done():
			p.reapErr = ctx.Err()
		}
	})
	return p.reapErr
}

func (p *IsolatedProcess) stopGroup(ctx context.Context, sig syscall.Signal, wait time.Duration) bool {
	if p.groupReaped() {
		return true
	}
	// A child can exit between the existence check and the signal. Poll the
	// group until the grace interval expires, including when signaling fails.
	_ = syscall.Kill(-p.PID, sig)
	timer := time.NewTimer(max(0, wait))
	defer timer.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for !p.groupReaped() {
		select {
		case <-ctx.Done():
			return p.groupReaped()
		case <-timer.C:
			return p.groupReaped()
		case <-ticker.C:
		}
	}
	return true
}

func (p *IsolatedProcess) groupReaped() bool {
	select {
	case <-p.done:
		return !daemon.ProcessGroupExists(p.PID)
	default:
		return false
	}
}

func (p *IsolatedProcess) diagnostics() []byte {
	select {
	case <-p.drained:
		return p.diagnostic.Bytes()
	default:
		return nil
	}
}

var secretPattern = regexp.MustCompile(`(?i)(bearer[ \t]+[^\s"'\x60,;]+|sk-[a-z0-9_\-]+|["']?(?:x-api-key|authorization|api[_-]?key|access[_-]?token|refresh[_-]?token|password)["']?\s*[:=]\s*(?:"[^"]*"|'[^']*'|(?:Bearer\s+)?[^\s,;]+))`)

func ScrubSecrets(s string) string { return secretPattern.ReplaceAllString(s, "[redacted]") }

type workspace struct{ root, home, private, task string }

func createWorkspace(opts ExecOptions, privateName string) (workspace, error) {
	root, err := os.MkdirTemp(opts.WorkspaceRoot, opts.TempPrefix+"-")
	if err != nil {
		return workspace{}, err
	}
	w := workspace{root, filepath.Join(root, "home"), filepath.Join(root, privateName), filepath.Join(root, "task")}
	for _, path := range []string{w.home, w.private, w.task} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			return workspace{}, errors.Join(err, os.RemoveAll(root))
		}
	}
	return w, nil
}

func (w workspace) environment(opts ExecOptions, extra ...string) []string {
	env := []string{"HOME=" + w.home, "TMPDIR=" + w.root, "PATH=" + buildChildPath(opts)}
	for _, key := range []string{"LANG", "LC_ALL", "HTTPS_PROXY", "HTTP_PROXY", "ALL_PROXY", "NO_PROXY"} {
		if value := opts.Environ(key); value != "" {
			env = append(env, key+"="+value)
		}
	}
	env = append(env, extra...)
	return append(env, opts.ProcessingEnvKey+"=1")
}

// buildChildPath keeps the isolated bridge PATH minimal while preserving the
// pre-symlink launcher directory (e.g. nvm/volta/Homebrew bin holding `node`)
// and, for `#!/usr/bin/env <interp>` scripts, the single directory owning that
// interpreter if it is not already reachable in the minimal PATH.
func buildChildPath(opts ExecOptions) string {
	seen := make(map[string]bool, 6)
	dirs := make([]string, 0, 6)
	addDir := func(dir string) {
		dir = strings.TrimSpace(dir)
		if dir == "" || !filepath.IsAbs(dir) {
			return
		}
		dir = filepath.Clean(dir)
		if seen[dir] {
			return
		}
		seen[dir] = true
		dirs = append(dirs, dir)
	}
	if opts.Executable != "" {
		addDir(filepath.Dir(opts.Executable))
	}
	addDir(opts.LauncherDir)
	if interp := envShebangInterpreter(opts.Executable); interp != "" {
		if !executableInDirs(interp, append(append([]string(nil), dirs...), "/usr/bin", "/bin")) && opts.Environ != nil {
			if dir := findInterpreterDir(interp, opts.Environ("PATH")); dir != "" {
				addDir(dir)
			}
		}
	}
	addDir("/usr/bin")
	addDir("/bin")
	return strings.Join(dirs, string(os.PathListSeparator))
}

func envShebangInterpreter(path string) string {
	if path == "" {
		return ""
	}
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	var buf [256]byte
	n, _ := f.Read(buf[:])
	header := buf[:n]
	if !bytes.HasPrefix(header, []byte("#!")) {
		return ""
	}
	if idx := bytes.IndexByte(header, '\n'); idx >= 0 {
		header = header[:idx]
	}
	fields := strings.Fields(strings.TrimSpace(string(header[2:])))
	if len(fields) < 2 || (fields[0] != "/usr/bin/env" && fields[0] != "/bin/env") {
		return ""
	}
	for _, token := range fields[1:] {
		if strings.HasPrefix(token, "-") || strings.Contains(token, "=") {
			continue
		}
		if strings.ContainsAny(token, "/\\\x00") {
			return ""
		}
		return token
	}
	return ""
}

func isExecutableRegular(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0
}

func executableInDirs(name string, dirs []string) bool {
	for _, dir := range dirs {
		if dir != "" && isExecutableRegular(filepath.Join(dir, name)) {
			return true
		}
	}
	return false
}

func findInterpreterDir(name, rawPath string) string {
	for _, dir := range filepath.SplitList(rawPath) {
		dir = strings.TrimSpace(dir)
		if dir == "" || !filepath.IsAbs(dir) {
			continue
		}
		if isExecutableRegular(filepath.Join(dir, name)) {
			if resolved, err := filepath.EvalSymlinks(dir); err == nil && filepath.IsAbs(resolved) {
				return filepath.Clean(resolved)
			}
			return filepath.Clean(dir)
		}
	}
	return ""
}

type transport struct {
	process                         *IsolatedProcess
	kind                            string
	lineLimit, totalLimit, received int
}

func (t *transport) read(ctx context.Context) (map[string]any, error) {
	line, err := ReadLineWithIdle(ctx, t.process.Stdout, t.process.Clock, t.lineLimit)
	if err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, errLineLimit) {
			if t.kind == "codex" {
				if errors.Is(err, errLineLimit) {
					return nil, fault("codex_output_limit")
				}
				return nil, fault("codex_transport_closed")
			}
			return nil, fault(t.kind + "_transport_closed_or_limit")
		}
		return nil, t.ioError(err)
	}
	t.received += len(line)
	if t.received > t.totalLimit {
		return nil, fault(t.kind + "_output_limit")
	}
	var row map[string]any
	if decode(line, &row) != nil || row == nil {
		return nil, fault(t.kind + "_transport_invalid")
	}
	return row, nil
}

func (t *transport) send(ctx context.Context, row map[string]any) error {
	encoded, err := json.Marshal(row)
	if err != nil {
		return fault(t.kind + "_transport_invalid")
	}
	if err := WriteAllWithIdle(ctx, t.process.Stdin, append(encoded, '\n'), t.process.Clock); err != nil {
		return t.ioError(err)
	}
	return nil
}

func (t *transport) ioError(err error) error {
	switch {
	case errors.Is(err, ErrIdleTimeout):
		return fault(t.kind+"_timeout", 504)
	case errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded):
		return fault(t.kind+"_cancelled", 499)
	default:
		return fault(t.kind + "_transport_closed")
	}
}

func (t *transport) interrupted(err error) bool {
	var failure *BridgeError
	return errors.As(err, &failure) && (failure.Status == 499 || failure.Status == 504)
}

func (t *transport) interrupt(row map[string]any) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	payload, err := json.Marshal(row)
	if err != nil {
		return
	}
	_ = t.process.Stdin.SetWriteDeadline(time.Time{})
	_ = WriteAllWithIdle(ctx, t.process.Stdin, append(payload, '\n'), NewActivityClock(2*time.Second))
}

func (t *transport) cleanup(err *error) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 7*time.Second)
	defer cancel()
	if reapErr := t.process.Reap(ctx, 3*time.Second, 3*time.Second); reapErr != nil {
		*err = fault(t.kind+"_process_cleanup_unknown", 503)
		return false
	}
	return true
}

func startTransport(ctx context.Context, opts ExecOptions, w workspace, kind string, args, env []string, lineLimit, totalLimit int) (*transport, error) {
	cmd := exec.Command(opts.Executable, args...)
	cmd.Dir, cmd.Env = w.task, env
	p, err := StartIsolatedProcess(ctx, cmd, NewActivityClock(opts.IdleTimeout))
	if err != nil {
		if ctx.Err() != nil {
			return nil, fault(kind+"_cancelled", 499)
		}
		return nil, fault(kind+"_process_unavailable", 503)
	}
	return &transport{process: p, kind: kind, lineLimit: lineLimit, totalLimit: totalLimit}, nil
}

func encodeJSON(v any) string {
	bytes, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(bytes)
}

func writePrivate(path string, data []byte, mode os.FileMode) error {
	if err := os.WriteFile(path, data, mode); err != nil {
		return fmt.Errorf("private workspace write: %w", err)
	}
	return nil
}
