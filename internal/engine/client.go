package engine

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
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
)

type Config struct {
	Executable         string
	DataDir            string
	ModelDir           string
	Library            string
	ExpectedVersion    string
	ExpectedGeneration string
	DenyNetwork        bool // macOS P0 offline verification; applies only to worker process.
}

type RemoteError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *RemoteError) Error() string { return e.Code + ": " + e.Message }

type response struct {
	Protocol   int             `json:"protocol"`
	ID         string          `json:"id"`
	Event      string          `json:"event"`
	OK         bool            `json:"ok"`
	Ready      bool            `json:"ready"`
	Version    string          `json:"worker_version"`
	Generation string          `json:"generation"`
	Result     json.RawMessage `json:"result"`
	Error      *RemoteError    `json:"error"`
}

type Client struct {
	cmd             *exec.Cmd
	stdin           io.WriteCloser
	writeMu         sync.Mutex
	mu              sync.Mutex
	pending         map[string]chan response
	ready           chan error
	done            chan struct{}
	err             error
	closeOnce       sync.Once
	config          Config
	Generation      string
	CodeRecipe      string
	CodeRootModules bool
	codeRecycled    bool
	diagnostic      boundedLog
}

type boundedLog struct {
	mu   sync.Mutex
	data bytes.Buffer
}

func (l *boundedLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if remain := 8192 - l.data.Len(); remain > 0 {
		l.data.Write(p[:min(remain, len(p))])
	}
	return len(p), nil
}
func (l *boundedLog) String() string { l.mu.Lock(); defer l.mu.Unlock(); return l.data.String() }

func Start(ctx context.Context, cfg Config) (*Client, error) {
	for _, path := range []string{cfg.Executable, cfg.DataDir, cfg.ModelDir, cfg.Library} {
		if !filepath.IsAbs(path) {
			return nil, errors.New("worker paths must be absolute")
		}
	}
	if cfg.ExpectedVersion == "" {
		cfg.ExpectedVersion = "0.0.1"
	}
	if err := os.MkdirAll(cfg.DataDir, 0700); err != nil {
		return nil, err
	}
	cmd := exec.Command(cfg.Executable, cfg.DataDir, cfg.ModelDir)
	if cfg.DenyNetwork {
		cmd = exec.Command("/usr/bin/sandbox-exec", "-p", "(version 1)(allow default)(deny network*)", cfg.Executable, cfg.DataDir, cfg.ModelDir)
	}
	cmd.Dir = cfg.DataDir
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + filepath.Join(cfg.DataDir, "private-home"), "RUST_LOG=error", "TELEMETRY_DISABLED=true", "ORT_DYLIB_PATH=" + cfg.Library}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	// Worker stderr contains diagnostics only; stdout is reserved for protocol.
	c := &Client{cmd: cmd, stdin: stdin, pending: map[string]chan response{}, ready: make(chan error, 1), done: make(chan struct{}), config: cfg}
	cmd.Stderr = &c.diagnostic
	if err = cmd.Start(); err != nil {
		stdin.Close()
		return nil, err
	}
	go c.read(stdout)
	select {
	case err = <-c.ready:
	case <-ctx.Done():
		err = ctx.Err()
	case <-c.done:
		err = errors.New("worker exited before ready")
	}
	if err != nil {
		c.Close()
		return nil, err
	}
	var handshake struct {
		Version         string `json:"worker_version"`
		Ready           bool   `json:"ready"`
		Generation      string `json:"generation"`
		CodeRecipe      string `json:"code_recipe"`
		CodeRootModules bool   `json:"code_root_modules"`
	}
	if err = c.Call(ctx, "handshake", nil, &handshake); err != nil {
		c.Close()
		return nil, err
	}
	if !handshake.Ready || handshake.Version != cfg.ExpectedVersion || handshake.Generation != c.Generation {
		c.Close()
		return nil, errors.New("worker handshake mismatch")
	}
	c.CodeRecipe = handshake.CodeRecipe
	c.CodeRootModules = handshake.CodeRootModules
	return c, nil
}

func (c *Client) read(stdout io.Reader) {
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 4096), 4*1024*1024)
	readySignalled := false
	for scanner.Scan() {
		var r response
		if err := json.Unmarshal(scanner.Bytes(), &r); err != nil || r.Protocol != 1 {
			c.mu.Lock()
			c.err = errors.New("invalid worker protocol output")
			c.mu.Unlock()
			c.kill(syscall.SIGKILL)
			break
		}
		if r.Event == "hello" && r.Version != c.config.ExpectedVersion {
			c.ready <- errors.New("incompatible worker version")
			readySignalled = true
			c.kill(syscall.SIGKILL)
			break
		}
		if r.Event == "ready" {
			if readySignalled {
				continue
			}
			readySignalled = true
			if !r.Ready || r.Version != c.config.ExpectedVersion || r.Generation == "" || (c.config.ExpectedGeneration != "" && r.Generation != c.config.ExpectedGeneration) {
				c.ready <- errors.New("incompatible worker model or generation")
				c.kill(syscall.SIGKILL)
				break
			}
			c.Generation = r.Generation
			c.ready <- nil
			continue
		}
		if r.ID == "" && r.Error != nil && !readySignalled {
			c.ready <- r.Error
			readySignalled = true
		}
		if r.ID != "" {
			c.mu.Lock()
			ch := c.pending[r.ID]
			c.mu.Unlock()
			if ch != nil {
				select {
				case ch <- r:
				default:
				}
			}
		}
	}
	err := scanner.Err()
	if err != nil {
		c.kill(syscall.SIGKILL)
	}
	waitErr := c.cmd.Wait()
	// Reap any native helper left in the worker's own process group.
	c.kill(syscall.SIGKILL)
	c.mu.Lock()
	c.err = errors.Join(c.err, err, waitErr)
	if c.err == nil {
		c.err = errors.New("worker stopped")
	}
	c.mu.Unlock()
	if !readySignalled {
		c.ready <- errors.New("worker exited before ready")
	}
	close(c.done)
}

func (c *Client) write(input map[string]any) error {
	data, err := json.Marshal(input)
	if err != nil {
		return err
	}
	if len(data) > 1024*1024-1 {
		return errors.New("worker request exceeds 1 MiB")
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_, err = c.stdin.Write(append(data, '\n'))
	return err
}

func (c *Client) Call(ctx context.Context, op string, fields map[string]any, out any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	id := uuid.NewString()
	input := map[string]any{}
	for key, value := range fields {
		input[key] = value
	}
	input["protocol"] = 1
	input["id"] = id
	input["op"] = op
	if deadline, ok := ctx.Deadline(); ok {
		input["timeout_ms"] = max(1, min(120000, time.Until(deadline).Milliseconds()))
	}
	ch := make(chan response, 1)
	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()
	defer func() { c.mu.Lock(); delete(c.pending, id); c.mu.Unlock() }()
	written := make(chan error, 1)
	go func() { written <- c.write(input) }()
	select {
	case err := <-written:
		if err != nil {
			return err
		}
	case <-ctx.Done():
		go c.write(map[string]any{"protocol": 1, "id": uuid.NewString(), "op": "cancel", "target": id})
		return ctx.Err()
	case <-c.done:
		return errors.New("worker stopped during request write")
	}
	select {
	case r := <-ch:
		if !r.OK {
			if r.Error != nil {
				return r.Error
			}
			return errors.New("worker operation failed")
		}
		if out == nil {
			return nil
		}
		return json.Unmarshal(r.Result, out)
	case <-ctx.Done():
		// Cancellation is an independent message, never queued behind the job.
		go c.write(map[string]any{"protocol": 1, "id": uuid.NewString(), "op": "cancel", "target": id})
		return ctx.Err()
	case <-c.done:
		c.mu.Lock()
		err := c.err
		c.mu.Unlock()
		return fmt.Errorf("worker unavailable: %w", err)
	}
}

func (c *Client) Index(ctx context.Context, r Record) (Receipt, error) {
	var receipt Receipt
	err := c.Call(ctx, "index", map[string]any{"record": r}, &receipt)
	if err == nil && (receipt.Generation != c.Generation || len(receipt.ChunkIDs) == 0) {
		return Receipt{}, errors.New("invalid worker index receipt")
	}
	return receipt, err
}

func (c *Client) Search(ctx context.Context, dataset, query string, k int) ([]Candidate, error) {
	var result struct {
		Candidates []Candidate `json:"candidates"`
	}
	err := c.Call(ctx, "search", map[string]any{"dataset": dataset, "query": query, "top_k": k}, &result)
	return result.Candidates, err
}

func (c *Client) kill(signal syscall.Signal) {
	if c.cmd.Process != nil {
		_ = syscall.Kill(-c.cmd.Process.Pid, signal)
	}
}

func (c *Client) Close() error {
	c.closeOnce.Do(func() {
		select {
		case <-c.done:
			return
		default:
		}
		go func() {
			_ = c.write(map[string]any{"protocol": 1, "id": uuid.NewString(), "op": "shutdown"})
			c.stdin.Close()
		}()
		select {
		case <-c.done:
		case <-time.After(time.Second):
			c.kill(syscall.SIGKILL)
			c.stdin.Close()
			<-c.done
		}
	})
	return nil
}

// PID is exposed for the owning supervisor's health and process-tree checks.
func (c *Client) PID() int {
	if c.cmd.Process == nil {
		return 0
	}
	return c.cmd.Process.Pid
}
func (c *Client) Done() <-chan struct{} { return c.done }
func (c *Client) Diagnostics() string   { return c.diagnostic.String() }

// CodeRecycled distinguishes a controlled native cancellation fence from a
// crash, so normal user cancellation does not consume the supervisor budget.
func (c *Client) CodeRecycled() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.codeRecycled
}
