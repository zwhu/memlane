package agents

import (
	"context"
	"time"
)

// CompletionDiagnostics is an observation, never a prompt, output, stderr,
// provider reason or credential. Absent on old receipts means unknown.
type CompletionDiagnostics struct {
	Stage               string `json:"stage"`
	FailureCode         string `json:"failure_code,omitempty"`
	ElapsedMS           int64  `json:"elapsed_ms"`
	RPCMS               int64  `json:"rpc_ms,omitempty"`
	ParentContext       string `json:"parent_context"`
	RPCContext          string `json:"rpc_context,omitempty"`
	Accepted            bool   `json:"accepted"`
	MessageComplete     bool   `json:"message_complete"`
	AgentEnded          bool   `json:"agent_ended"`
	Settled             bool   `json:"settled"`
	FinalState          bool   `json:"final_state"`
	AssistantStopReason string `json:"assistant_stop_reason,omitempty"`
}

func CompletionContextState(err error) string {
	switch err {
	case nil:
		return "active"
	case context.DeadlineExceeded:
		return "deadline_exceeded"
	case context.Canceled:
		return "cancelled"
	default:
		return "unknown"
	}
}
func completionStopReason(value string) string {
	switch value {
	case "stop", "toolUse", "error", "aborted", "length":
		return value
	case "":
		return ""
	default:
		return "unknown"
	}
}

// SafeCompletionDiagnostics copies only fixed compiler-owned enum values.
func SafeCompletionDiagnostics(value *CompletionDiagnostics) *CompletionDiagnostics {
	if value == nil {
		return nil
	}
	d := *value
	switch d.Stage {
	case "unknown", "completed", "preflight", "rpc_start", "rpc_setup", "rpc_stream", "rpc_tail", "rpc_write", "rpc_read", "rpc_json", "assistant", "output_schema", "cleanup":
	default:
		d.Stage = "unknown"
	}
	switch d.FailureCode {
	case "", "unknown", "preflight_failed", "process_start_failed", "process_exit_failed", "exit_timeout", "rpc_write_failed", "rpc_read_failed", "rpc_eof", "rpc_protocol_invalid", "rpc_invalid_json", "assistant_length", "assistant_error", "assistant_aborted", "assistant_terminal_invalid", "deadline_exceeded", "cancelled", "cleanup_failed", "native_schema_unconfirmed", "output_schema_invalid":
	default:
		d.FailureCode = "unknown"
	}
	for _, field := range []*string{&d.ParentContext, &d.RPCContext} {
		switch *field {
		case "", "active", "deadline_exceeded", "cancelled", "unknown":
		default:
			*field = "unknown"
		}
	}
	d.AssistantStopReason = completionStopReason(d.AssistantStopReason)
	for _, field := range []*int64{&d.ElapsedMS, &d.RPCMS} {
		if *field < 0 {
			*field = 0
		}
		if *field > int64((24*time.Hour)/time.Millisecond) {
			*field = int64((24 * time.Hour) / time.Millisecond)
		}
	}
	return &d
}
