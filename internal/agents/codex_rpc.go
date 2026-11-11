//go:build darwin || linux

package agents

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

// One fresh stdio process per task. The wire data stays in memory; config/read
// can contain provider credentials and must never enter diagnostic transcripts.
type codexRPCMessage struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  json.RawMessage `json:"error"`
}
type codexRPCRead struct {
	message codexRPCMessage
	err     error
}
type codexRPC struct {
	observe                func(codexRPCMessage) // optional bounded protocol-only observation
	cmd                    *exec.Cmd
	input                  io.WriteCloser
	output                 io.ReadCloser
	control                *os.File
	events                 chan codexRPCRead
	commands               chan []byte
	writeErrors            chan error
	stop                   chan struct{}
	readerDone, writerDone chan struct{}
	nextID                 int
	started                int64
}

func startCodexRPC(ctx context.Context, executable, guard string, args, env []string, dir string, onStart func(int) error) (*codexRPC, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !filepath.IsAbs(executable) || !filepath.IsAbs(dir) {
		return nil, errors.New("absolute Codex executable and task directory required")
	}
	var controlRead, controlWrite *os.File
	if guard != "" {
		if !filepath.IsAbs(guard) {
			return nil, errors.New("absolute task guard required")
		}
		var err error
		controlRead, controlWrite, err = os.Pipe()
		if err != nil {
			return nil, err
		}
		defer controlRead.Close()
		args = append([]string{"--proposal-guard", executable}, args...)
		executable = guard
	}
	// The caller sends turn/interrupt before group teardown. A background
	// CommandContext permits the shared Cancel hook without racing that handshake.
	cmd := exec.CommandContext(context.Background(), executable, args...)
	cmd.Dir, cmd.Env = dir, env
	configureAgentProcess(cmd)
	if controlRead != nil {
		cmd.ExtraFiles = []*os.File{controlRead}
	}
	input, err := cmd.StdinPipe()
	if err != nil {
		if controlWrite != nil {
			controlWrite.Close()
		}
		return nil, err
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		input.Close()
		if controlWrite != nil {
			controlWrite.Close()
		}
		return nil, err
	}
	// Only generic protocol errors are surfaced; arbitrary Agent diagnostics can
	// contain login/config values. Synthetic verifiers keep their own evidence.
	cmd.Stderr = io.Discard
	if err = cmd.Start(); err != nil {
		input.Close()
		output.Close()
		if controlWrite != nil {
			controlWrite.Close()
		}
		return nil, err
	}
	r := &codexRPC{cmd: cmd, input: input, output: output, control: controlWrite,
		events: make(chan codexRPCRead, 8), commands: make(chan []byte, 8), writeErrors: make(chan error, 1),
		stop: make(chan struct{}), readerDone: make(chan struct{}), writerDone: make(chan struct{}), started: time.Now().UnixMilli()}
	if onStart != nil {
		err = onStart(cmd.Process.Pid)
	}
	if err == nil && controlWrite != nil {
		_, err = controlWrite.Write([]byte{'S'})
	}
	if err != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		input.Close()
		output.Close()
		if controlWrite != nil {
			controlWrite.Close()
		}
		_ = cmd.Wait()
		_ = finishAgentProcess(cmd)
		return nil, err
	}
	go func() {
		defer close(r.writerDone)
		for {
			select {
			case <-r.stop:
				return
			case b := <-r.commands:
				if _, e := input.Write(b); e != nil {
					r.writeErrors <- errors.New("Codex RPC write failed")
					return
				}
			}
		}
	}()
	go func() {
		defer close(r.readerDone)
		stream := &io.LimitedReader{R: output, N: 64*1024*1024 + 1}
		scan := bufio.NewScanner(stream)
		scan.Buffer(make([]byte, 4096), 8*1024*1024)
		deliver := func(e codexRPCRead) bool {
			select {
			case r.events <- e:
				return true
			case <-r.stop:
				return false
			}
		}
		for scan.Scan() {
			var m codexRPCMessage
			if json.Unmarshal(scan.Bytes(), &m) != nil || m.Method == "" && len(m.ID) == 0 {
				deliver(codexRPCRead{err: errors.New("invalid Codex RPC message")})
				return
			}
			if !deliver(codexRPCRead{message: m}) {
				return
			}
		}
		deliver(codexRPCRead{err: errors.New("Codex RPC stream ended before task completion")})
	}()
	return r, nil
}

func (r *codexRPC) send(ctx context.Context, value any) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(b) > 8*1024*1024 {
		return errors.New("Codex RPC request exceeded resource limit")
	}
	select {
	case r.commands <- append(b, '\n'):
		return nil
	case e := <-r.writeErrors:
		return e
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (r *codexRPC) next(ctx context.Context) (codexRPCMessage, error) {
	select {
	case e := <-r.events:
		if r.observe != nil && e.err == nil {
			r.observe(e.message)
		}
		return e.message, e.err
	case e := <-r.writeErrors:
		return codexRPCMessage{}, e
	case <-ctx.Done():
		return codexRPCMessage{}, ctx.Err()
	}
}
func (r *codexRPC) call(ctx context.Context, method string, params any, result any) error {
	r.nextID++
	id := r.nextID
	if err := r.send(ctx, map[string]any{"id": id, "method": method, "params": params}); err != nil {
		return err
	}
	for {
		m, err := r.next(ctx)
		if err != nil {
			return err
		}
		if len(m.ID) != 0 && m.Method != "" {
			return errors.New("unexpected Codex server request before task start")
		}
		var got int
		if json.Unmarshal(m.ID, &got) == nil && got == id {
			if len(m.Error) != 0 {
				return errors.New("Codex rejected " + method)
			}
			if len(m.Result) == 0 {
				return errors.New("Codex result missing")
			}
			decoder := json.NewDecoder(bytes.NewReader(m.Result))
			decoder.UseNumber()
			return decoder.Decode(result)
		}
	}
}
func (r *codexRPC) initialize(ctx context.Context) error {
	var result json.RawMessage
	if err := r.call(ctx, "initialize", map[string]any{"clientInfo": map[string]string{"name": "memlane_project_organizer", "version": "1"}, "capabilities": map[string]bool{"experimentalApi": true}}, &result); err != nil {
		return err
	}
	return r.send(ctx, map[string]any{"method": "initialized", "params": map[string]any{}})
}

func (r *codexRPC) close(out *ProposalProcess) error {
	close(r.stop)
	_ = r.input.Close()
	if r.control != nil {
		_ = r.control.Close()
	}
	// The owner always closes the group, including descendants holding pipes.
	_ = syscall.Kill(-r.cmd.Process.Pid, syscall.SIGKILL)
	_ = r.output.Close()
	_ = r.cmd.Wait()
	<-r.readerDone
	<-r.writerDone
	gone := finishAgentProcess(r.cmd)
	if out != nil {
		out.Started, out.PID, out.StartedMS = true, r.cmd.Process.Pid, r.started
		out.Reaped, out.GroupGone, out.EndedMS = r.cmd.ProcessState != nil, gone, time.Now().UnixMilli()
		if out.Reaped {
			out.ExitCode = r.cmd.ProcessState.ExitCode()
		}
	}
	if !gone {
		return errors.New("Codex task process group remains active")
	}
	return nil
}
