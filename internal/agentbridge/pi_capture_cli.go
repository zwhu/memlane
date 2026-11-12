package agentbridge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
)

type piCaptureCall func(context.Context, string, serviceipc.Request, any) error

// This host-only command has no memory tool registration, consent mutation or
// outbox. Raw input exists only on stdin/in memory and never in process argv.
func RunPiCaptureCLI(args []string) int {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	return runPiCaptureCLI(ctx, args, os.Stdin, os.Stdout, os.Getenv("MEMLANE_TOOL_MODE"), os.Getenv("MEMLANE_USAGE_CHANNEL"), serviceipc.Call)
}

func runPiCaptureCLI(ctx context.Context, args []string, input io.Reader, output io.Writer, toolMode, channel string, call piCaptureCall) int {
	fail := func(code string) int {
		_ = json.NewEncoder(output).Encode(Result{Protocol: 1, Error: &Failure{Code: code, Message: "pi capture request could not be verified"}})
		return 1
	}
	if toolMode != "" || channel != "" && channel != "daily" {
		return fail("capture_isolated")
	}
	if len(args) == 0 || (args[0] != "authorize" && args[0] != "submit" && args[0] != "inspect") {
		return fail("invalid_request")
	}
	flags := flag.NewFlagSet("pi-capture", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var root, connection, workspace, project string
	var generation int64
	flags.StringVar(&root, "root", "", "")
	flags.StringVar(&connection, "connection", "", "")
	flags.Int64Var(&generation, "generation", 0, "")
	flags.StringVar(&workspace, "workspace", "", "")
	flags.StringVar(&project, "project", "", "")
	if flags.Parse(args[1:]) != nil || flags.NArg() != 0 || !filepath.IsAbs(root) || !filepath.IsAbs(workspace) || generation < 1 {
		return fail("invalid_request")
	}
	if id, err := uuid.Parse(connection); err != nil || id.String() != connection {
		return fail("invalid_request")
	}
	if project != "" {
		if id, err := uuid.Parse(project); err != nil || id.String() != project {
			return fail("invalid_request")
		}
	}
	raw, err := io.ReadAll(io.LimitReader(input, 1024*1024+1))
	if err != nil || len(raw) > 1024*1024 {
		return fail("invalid_request")
	}
	if trimmed := bytes.TrimSpace(raw); len(trimmed) == 0 || trimmed[0] != '{' {
		return fail("invalid_request")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var payload struct {
		Batch       string                         `json:"batch"`
		Grant       string                         `json:"grant"`
		HostSession string                         `json:"host_session"`
		Entries     []records.OfficialCaptureInput `json:"entries"`
	}
	if decoder.Decode(&payload) != nil {
		return fail("invalid_request")
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		return fail("invalid_request")
	}
	request := records.AgentRequest{Protocol: 1, ConnectionID: connection, Generation: generation, Workspace: workspace, Tool: "memory_save", Channel: "daily", Arguments: records.AgentArguments{Scope: project}}
	op := "pi_capture_" + args[0]
	envelope := serviceipc.Request{Op: op, AgentRequest: request}
	if args[0] == "authorize" {
		if payload.Batch != "" || payload.Grant != "" || payload.HostSession != "" || len(payload.Entries) != 0 {
			return fail("invalid_request")
		}
	} else {
		envelope.PiCaptureBatch = &records.PiCaptureBatchRequest{Batch: payload.Batch, Grant: payload.Grant, HostSession: payload.HostSession, Entries: payload.Entries, Request: request}
		// The daemon scanner is bounded to 1 MiB including the IPC envelope.
		encoded, e := json.Marshal(envelope)
		if e != nil || len(encoded) > 1000*1024 {
			return fail("capture_range_exceeded")
		}
	}
	operation, cancel := context.WithTimeout(ctx, 115*time.Second)
	defer cancel()
	var result json.RawMessage
	if err = call(operation, root, envelope, &result); err != nil {
		code := "service_unavailable"
		var service *serviceipc.ServiceError
		if errors.As(err, &service) {
			code = service.Code
		}
		if errors.Is(err, context.Canceled) {
			code = "cancelled"
		}
		if errors.Is(err, context.DeadlineExceeded) {
			code = "timeout"
		}
		if len(code) == 0 || len(code) > 64 {
			code = "service_error"
		}
		for _, letter := range code {
			if letter != '_' && (letter < 'a' || letter > 'z') {
				code = "service_error"
				break
			}
		}
		return fail(code)
	}
	if !json.Valid(result) {
		return fail("invalid_response")
	}
	if json.NewEncoder(output).Encode(Result{Protocol: 1, OK: true, Result: result}) != nil {
		return 1
	}
	return 0
}
