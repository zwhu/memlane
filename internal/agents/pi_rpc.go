//go:build darwin || linux

package agents

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
)

type PiRPCProcess struct {
	Diagnostics *CompletionDiagnostics `json:"diagnostics,omitempty"`
	ProposalProcess
	FailureStage               string `json:"failure_stage,omitempty"` // bounded runner stage, never paths/output
	FailureCode                string `json:"failure_code,omitempty"`  // bounded diagnostic, never original error text
	FailureItem                int    `json:"failure_item,omitempty"`  // bounded candidate position, no payload
	Accepted                   bool   `json:"prompt_accepted"`
	AbortSent                  bool   `json:"abort_sent"`
	AbortAcknowledged          bool   `json:"abort_acknowledged"`
	ResolvedProvider           string `json:"rpc_resolved_provider,omitempty"`
	ResolvedModel              string `json:"rpc_resolved_model,omitempty"`
	FinalFormat                string `json:"final_format,omitempty"`
	TransportCandidate         string `json:"-"` // unvalidated; only synthetic probes may capture it
	NativeSchemaRequested      bool   `json:"native_schema_requested"`
	NativeSchemaResponseStatus int    `json:"native_schema_response_status,omitempty"`
	ProcessDiagnostic          string `json:"-"` // only synthetic probes may capture sanitized stderr
}

type piRPCRead struct {
	Event piRPCEvent
	Err   error
}

type piRPCEvent struct {
	ToolName     string `json:"toolName"`
	ToolCallID   string `json:"toolCallId"`
	Type         string `json:"type"`
	ID           string `json:"id"`
	Command      string `json:"command"`
	Success      *bool  `json:"success"`
	Error        string `json:"error"`
	WillRetry    *bool  `json:"willRetry"`
	SchemaDigest string `json:"schema_sha256"`
	Status       int    `json:"status"`
	Data         struct {
		Model struct {
			Provider string `json:"provider"`
			ID       string `json:"id"`
		} `json:"model"`
		IsStreaming *bool `json:"isStreaming"`
	} `json:"data"`
	Message struct {
		Role         string `json:"role"`
		ToolName     string `json:"toolName"`
		ToolCallID   string `json:"toolCallId"`
		StopReason   string `json:"stopReason"`
		ErrorMessage string `json:"errorMessage"`
		Content      []struct {
			Type string `json:"type"`
			Text string `json:"text"`
			Name string `json:"name"`
			ID   string `json:"id"`
		} `json:"content"`
	} `json:"message"`
}

// One prompt per fresh RPC process. Command acceptance does not release a
// proposal; terminal assistant output, agent_end and cleanup are all required.
func runPiRPCProcessOwned(parent context.Context, executable string, args, env []string, dir, prompt, guard string, onStart func(int) error, onEvent func(string), onModel func(string, string) error, schemaDigest string) (PiRPCProcess, error) {
	return runPiRPCProcessWithProject(parent, executable, args, env, dir, prompt, guard, onStart, onEvent, onModel, schemaDigest, nil)
}

func runPiRPCProcessWithProject(parent context.Context, executable string, args, env []string, dir, prompt, guard string, onStart func(int) error, onEvent func(string), onModel func(string, string) error, schemaDigest string, project *piProjectTools) (out PiRPCProcess, resultErr error) {
	var toolBoundary piProjectRPCBoundary
	diagnosticStart := time.Now()
	messageComplete, agentEnded, settled, started, promptSent, finalState := false, false, false, false, false, false
	phase, cause, stopReason := "rpc_start", "", ""

	out.Events = map[string]int{}
	out.Items = map[string]int{}
	out.ExitCode = -1
	ctx, cancel := context.WithTimeout(parent, 2*time.Minute)
	defer cancel()
	defer func() {
		if resultErr != nil && cause == "" {
			switch {
			case errors.Is(resultErr, context.DeadlineExceeded):
				cause = "deadline_exceeded"
			case errors.Is(resultErr, context.Canceled):
				cause = "cancelled"
			case !out.Started:
				cause = "process_start_failed"
			default:
				cause = "rpc_protocol_invalid"
			}
		}
		if resultErr == nil {
			phase = "completed"
		} else if phase == "rpc_stream" && (messageComplete || agentEnded || settled) {
			phase = "rpc_tail"
		}
		elapsed := time.Since(diagnosticStart).Milliseconds()
		out.Diagnostics = SafeCompletionDiagnostics(&CompletionDiagnostics{Stage: phase, FailureCode: cause, ElapsedMS: elapsed, RPCMS: elapsed, ParentContext: CompletionContextState(parent.Err()), RPCContext: CompletionContextState(ctx.Err()), Accepted: out.Accepted, MessageComplete: messageComplete, AgentEnded: agentEnded, Settled: settled, FinalState: finalState, AssistantStopReason: stopReason})
	}()
	var controlRead, controlWrite *os.File
	if guard != "" {
		if !strings.HasPrefix(guard, "/") {
			return out, errors.New("absolute RPC guard required")
		}
		var err error
		controlRead, controlWrite, err = os.Pipe()
		if err != nil {
			return out, err
		}
		defer controlRead.Close()
		defer controlWrite.Close()
		args = append([]string{"--proposal-guard", executable}, args...)
		executable = guard
	}
	cmd := exec.Command(executable, args...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = time.Second
	if controlRead != nil {
		cmd.ExtraFiles = []*os.File{controlRead}
	}
	input, err := cmd.StdinPipe()
	if err != nil {
		return out, err
	}
	defer input.Close()
	output, err := cmd.StdoutPipe()
	if err != nil {
		return out, err
	}
	var diagnostic diagnosticOutput
	cmd.Stderr = &diagnostic
	if err = cmd.Start(); err != nil {
		return out, err
	}
	out.Started = true
	out.StartedMS = time.Now().UnixMilli()
	out.PID = cmd.Process.Pid
	// Every started process reaches one Wait, including registration failures.
	defer func() {
		_ = input.Close()
		waited := make(chan error, 1)
		go func() { waited <- cmd.Wait() }()
		select {
		case e := <-waited:
			if resultErr == nil && e != nil {
				resultErr = e
				if cause == "" {
					phase, cause = "cleanup", "process_exit_failed"
				}
			}
		case <-time.After(time.Second):
			_ = syscall.Kill(-out.PID, syscall.SIGKILL)
			e := <-waited
			if resultErr == nil {
				resultErr = errors.Join(errors.New("RPC exit timed out"), e)
				if cause == "" {
					phase, cause = "cleanup", "exit_timeout"
				}
			}
		}
		out.Reaped = cmd.ProcessState != nil
		if out.Reaped {
			out.ExitCode = cmd.ProcessState.ExitCode()
		}
		out.EndedMS = time.Now().UnixMilli()
		out.GroupGone = finishAgentProcess(cmd)
		out.ProcessDiagnostic = safeReason(diagnostic.text.String())
		if schemaDigest != "" {
			bound, responses := 0, 0
			// pi 0.84.1 redirects Extension stdout/console output to stderr.
			// Parse only our bounded metadata records; never persist payloads or headers.
			for _, line := range strings.Split(diagnostic.text.String(), "\n") {
				var e piRPCEvent
				if json.Unmarshal([]byte(line), &e) != nil || e.SchemaDigest != schemaDigest {
					continue
				}
				switch e.Type {
				case "memlane_schema_bound":
					bound++
				case "memlane_schema_response":
					responses++
					out.NativeSchemaResponseStatus = e.Status
				}
			}
			out.NativeSchemaRequested = bound == 1
			if resultErr == nil && (!out.NativeSchemaRequested || responses != 1 || out.NativeSchemaResponseStatus != 200) {
				resultErr = errors.New("native schema provider receipt missing or repeated")
				if cause == "" {
					phase, cause = "rpc_tail", "native_schema_unconfirmed"
				}
			}
		}

		if !out.GroupGone {
			resultErr = errors.Join(resultErr, errors.New("RPC process group still active"))
			if cause == "" {
				phase, cause = "cleanup", "cleanup_failed"
			}
		}
		if resultErr != nil {
			out.Final = nil
			out.Failure = safeReason(resultErr.Error())
		}
	}()
	if controlRead != nil {
		_ = controlRead.Close()
	}
	if onStart != nil {
		if err = onStart(out.PID); err != nil {
			_ = syscall.Kill(-out.PID, syscall.SIGKILL)
			return out, err
		}
	}
	if controlWrite != nil {
		if _, err = controlWrite.Write([]byte{'S'}); err != nil {
			_ = syscall.Kill(-out.PID, syscall.SIGKILL)
			return out, err
		}
	}
	commands := make(chan []byte, 8)
	writeErrors := make(chan error, 1)
	writerDone := make(chan struct{})
	writerStop := make(chan struct{})
	go func() {
		defer close(writerDone)
		for {
			select {
			case <-writerStop:
				return
			case data := <-commands:
				if _, e := input.Write(data); e != nil {
					select {
					case writeErrors <- e:
					default:
					}
					return
				}
			}
		}
	}()
	defer func() { close(writerStop); _ = input.Close(); <-writerDone }()
	queue := func(id, kind string, extra map[string]any) {
		data := map[string]any{"id": id, "type": kind}
		for k, v := range extra {
			data[k] = v
		}
		b, _ := json.Marshal(data)
		commands <- append(b, '\n')
	}
	events := make(chan piRPCRead, 8)
	readerDone := make(chan struct{})
	readerStop := make(chan struct{})
	go func() {
		defer close(readerDone)
		stream := &io.LimitedReader{R: output, N: 8*1024*1024 + 1}
		scan := bufio.NewScanner(stream)
		scan.Buffer(make([]byte, 4096), 1024*1024)
		for scan.Scan() {
			var event piRPCEvent
			if e := json.Unmarshal(scan.Bytes(), &event); e != nil || event.Type == "" {
				select {
				case events <- piRPCRead{Err: errors.New("invalid RPC JSONL event")}:
				case <-readerStop:
				}
				return
			}
			select {
			case events <- piRPCRead{Event: event}:
			case <-readerStop:
				return
			}
		}
		e := scan.Err()
		if e == nil {
			e = io.EOF
		}
		if stream.N <= 0 {
			e = errors.New("RPC event output limit exceeded")
		}
		select {
		case events <- piRPCRead{Err: e}:
		case <-readerStop:
		}
	}()
	defer func() {
		// Allow pi's stdin EOF shutdown to finish before closing its stdout.
		_ = input.Close()
		select {
		case <-readerDone:
		case <-time.After(time.Second):
		}
		close(readerStop)
		_ = output.Close()
		<-readerDone
	}()
	stateID, retryID, compactID, promptID, abortID, finalStateID := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	expected := map[string]string{stateID: "get_state", retryID: "set_auto_retry", compactID: "set_auto_compaction", promptID: "prompt", abortID: "abort", finalStateID: "get_state"}
	seen := map[string]bool{}
	queue(stateID, "get_state", nil)
	var final string
	phase = "rpc_setup"
	var abortDeadline <-chan time.Time
	ctxDone := ctx.Done()
	fail := func(e error) (PiRPCProcess, error) {
		if cause == "" {
			switch {
			case errors.Is(e, context.DeadlineExceeded):
				cause = "deadline_exceeded"
			case errors.Is(e, context.Canceled):
				cause = "cancelled"
			case errors.Is(e, io.EOF):
				cause = "rpc_eof"
			default:
				cause = "rpc_protocol_invalid"
			}
		}
		_ = syscall.Kill(-out.PID, syscall.SIGKILL)
		return out, e
	}
	for {
		if ctx.Err() != nil && !out.Cancelled {
			out.Cancelled = true
			out.AbortSent = true
			queue(abortID, "abort", nil)
			ctxDone = nil
			abortDeadline = time.After(time.Second)
		}
		if out.Accepted && messageComplete && settled && finalState && !out.Cancelled {
			if e := ctx.Err(); e != nil {
				out.Cancelled = true
				return fail(e)
			}
			out.TransportCandidate = final
			candidate := strings.TrimSpace(final)
			out.TransportCandidate = final
			out.FinalFormat = "json"
			for _, opening := range []string{"```json\n", "```\n"} {
				if strings.HasPrefix(candidate, opening) && strings.HasSuffix(candidate, "\n```") {
					candidate = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(candidate, opening), "\n```"))
					out.FinalFormat = "single_json_fence"
					break
				}
			}
			if !json.Valid([]byte(candidate)) || len(final) > 256*1024 {
				phase, cause = "rpc_json", "rpc_invalid_json"
				out.FinalFormat = "invalid_json"
				if strings.HasPrefix(strings.TrimSpace(final), "```") {
					out.FinalFormat = "markdown_fence"
				}
				if len(final) > 256*1024 {
					out.FinalFormat = "too_large"
				}
				return fail(errors.New("RPC final structured result invalid: " + out.FinalFormat))
			}
			if project != nil {
				if err := toolBoundary.complete(); err != nil {
					return fail(err)
				}
			}
			out.Terminal = true
			out.Final = json.RawMessage(candidate)
			return out, nil
		}
		if out.Cancelled && out.AbortAcknowledged && (settled || !started) {
			return fail(ctx.Err())
		}
		select {
		case <-ctxDone:
			out.Cancelled = true
			out.AbortSent = true
			queue(abortID, "abort", nil)
			ctxDone = nil
			abortDeadline = time.After(time.Second)
		case <-abortDeadline:
			return fail(errors.Join(ctx.Err(), errors.New("RPC abort acknowledgement or terminal missing")))
		case e := <-writeErrors:
			phase, cause = "rpc_write", "rpc_write_failed"
			return fail(fmt.Errorf("RPC input failed: %w", e))
		case packet := <-events:
			if packet.Err != nil {
				if !errors.Is(packet.Err, io.EOF) {
					phase, cause = "rpc_read", "rpc_read_failed"
				}
				return fail(fmt.Errorf("RPC ended before complete result: %w", packet.Err))
			}
			event := packet.Event
			out.Events[event.Type]++
			if onEvent != nil {
				onEvent(event.Type)
			}
			switch event.Type {
			case "response":
				command, ok := expected[event.ID]
				if !ok || command != event.Command || seen[event.ID] || event.Success == nil {
					return fail(errors.New("RPC response identity or duplication invalid"))
				}
				seen[event.ID] = true
				if !*event.Success {
					return fail(errors.New("RPC rejected " + command + ": " + safeReason(event.Error)))
				}
				switch command {
				case "get_state":
					if event.Data.IsStreaming == nil || *event.Data.IsStreaming || event.Data.Model.ID == "" || event.Data.Model.Provider == "" {
						return fail(errors.New("RPC did not start with an idle selected model"))
					}
					if event.ID == finalStateID {
						if !settled || !messageComplete || event.Data.Model.ID != out.ResolvedModel || event.Data.Model.Provider != out.ResolvedProvider {
							return fail(errors.New("RPC final state or model changed"))
						}
						finalState = true
					} else {
						out.ResolvedModel = event.Data.Model.ID
						out.ResolvedProvider = event.Data.Model.Provider
						if onModel != nil {
							if err := onModel(out.ResolvedProvider, out.ResolvedModel); err != nil {
								return fail(err)
							}
						}
						if !out.Cancelled {
							queue(retryID, "set_auto_retry", map[string]any{"enabled": false})
							queue(compactID, "set_auto_compaction", map[string]any{"enabled": false})
						}
					}
				case "set_auto_retry", "set_auto_compaction":
					if seen[retryID] && seen[compactID] && !promptSent && !out.Cancelled {
						promptSent = true
						queue(promptID, "prompt", map[string]any{"message": prompt})
					}
				case "prompt":
					out.Accepted = true
					phase = "rpc_stream"
				case "abort":
					if !out.AbortSent {
						return fail(errors.New("unsolicited RPC abort response"))
					}
					out.AbortAcknowledged = true
				}
			case "memlane_schema_bound":
				if schemaDigest == "" || event.SchemaDigest != schemaDigest || !started || agentEnded || out.NativeSchemaRequested {
					return fail(errors.New("native schema binding invalid or repeated"))
				}
				out.NativeSchemaRequested = true
			case "memlane_schema_response":
				if !out.NativeSchemaRequested || event.SchemaDigest != schemaDigest || !started || agentEnded || out.NativeSchemaResponseStatus != 0 {
					return fail(errors.New("native schema response invalid"))
				}
				out.NativeSchemaResponseStatus = event.Status
			case "agent_start":
				if !promptSent || started {
					return fail(errors.New("RPC agent lifecycle invalid"))
				}
				started = true
			case "message_end":
				phase = "rpc_stream"
				if !started || agentEnded {
					return fail(errors.New("RPC message outside operation"))
				}
				if event.Message.Role == "assistant" {
					stopReason = completionStopReason(event.Message.StopReason)
					if project != nil && event.Message.StopReason == "toolUse" {
						if messageComplete {
							return fail(errors.New("pi project tool after final answer"))
						}
						if err := toolBoundary.assistant(event); err != nil {
							return fail(err)
						}
						continue
					}

					if out.Cancelled && event.Message.StopReason == "aborted" {
						continue
					}
					if project != nil && !out.Cancelled && !project.phaseEnded() {
						return fail(errors.New("pi project final answer before read barrier"))
					}
					if messageComplete || event.Message.StopReason != "stop" {
						phase, cause = "assistant", "assistant_terminal_invalid"
						switch event.Message.StopReason {
						case "length":
							cause = "assistant_length"
						case "error":
							cause = "assistant_error"
						case "aborted":
							cause = "assistant_aborted"
						}
						return fail(errors.New("RPC assistant failed, truncated or duplicated: " + safeReason(event.Message.ErrorMessage)))
					}
					for _, part := range event.Message.Content {
						switch part.Type {
						case "text":
							final += part.Text
						case "thinking":
						default:
							return fail(errors.New("RPC unexpected assistant tool/content"))
						}
					}
					messageComplete = true
					out.Items["assistant_message"]++
				} else if project != nil && event.Message.Role == "toolResult" {
					if err := toolBoundary.result(event); err != nil {
						return fail(err)
					}
				} else if event.Message.Role != "user" {
					return fail(errors.New("RPC unexpected message role"))
				}
			case "agent_end":
				if !started || agentEnded || event.WillRetry == nil || *event.WillRetry {
					return fail(errors.New("RPC operation end invalid"))
				}
				agentEnded = true
			case "agent_settled":
				if !agentEnded || settled {
					return fail(errors.New("RPC settle lifecycle invalid"))
				}
				settled = true
				if !out.Cancelled {
					queue(finalStateID, "get_state", nil)
				}
			case "turn_start", "turn_end", "message_start", "message_update":
				if !started || agentEnded {
					return fail(errors.New("RPC event outside operation"))
				}
			case "tool_execution_start", "tool_execution_end":
				if project == nil || !started || agentEnded {
					return fail(errors.New("RPC unexpected tool event"))
				}
				if err := toolBoundary.event(event); err != nil {
					return fail(err)
				}
			case "extension_error":
				return fail(errors.New("RPC Extension failed: " + safeReason(event.Error)))
			default:
				return fail(errors.New("RPC unexpected event or forbidden tool: " + event.Type))
			}
		}
	}
}
