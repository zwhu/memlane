package agents

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCompletionDiagnosticsActualRPC(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	script, err := filepath.Abs("testdata/pi_diagnostics_rpc.py")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		mode, code string
		tail       bool
	}{{"eof", "rpc_eof", false}, {"deadline", "deadline_exceeded", false}, {"missing-settle", "deadline_exceeded", true}, {"invalid-json", "rpc_invalid_json", true}, {"truncated", "assistant_length", false}} {
		t.Run(tc.mode, func(t *testing.T) {
			timeout := 3 * time.Second
			if tc.mode == "deadline" || tc.mode == "missing-settle" {
				timeout = 500 * time.Millisecond
			}
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			out, e := runPiRPCProcessOwned(ctx, python, []string{script, tc.mode}, []string{"PATH=/usr/bin:/bin"}, t.TempDir(), "synthetic only", "", nil, nil, nil, "fixed-schema")
			if e == nil || out.Terminal || !out.Reaped || !out.GroupGone || out.NativeSchemaResponseStatus != 200 {
				t.Fatal("wrong protocol seam", out, e)
			}
			d := out.Diagnostics
			if d == nil || d.FailureCode != tc.code || !d.Accepted || d.MessageComplete != tc.tail {
				t.Fatalf("wrong safe cause: %+v", d)
			}
			if tc.mode == "missing-settle" && (!d.AgentEnded || d.Settled || d.FinalState || d.Stage != "rpc_tail") {
				t.Fatalf("lost tail state: %+v", d)
			}
			if tc.mode == "truncated" && d.AssistantStopReason != "length" {
				t.Fatal(d)
			}
		})
	}
}

func TestCompletionDiagnosticsSafeAndLegacy(t *testing.T) {
	const secret = "private raw provider error"
	d := SafeCompletionDiagnostics(&CompletionDiagnostics{Stage: secret, FailureCode: secret, ParentContext: secret, RPCContext: secret, AssistantStopReason: secret, ElapsedMS: -1})
	raw, e := json.Marshal(d)
	if e != nil || strings.Contains(string(raw), secret) || d.ElapsedMS != 0 || d.Stage != "unknown" || d.FailureCode != "unknown" {
		t.Fatal("unsafe diagnostic", string(raw), e)
	}
	var old PiRPCProcess
	if e = json.Unmarshal([]byte(`{"terminal":false}`), &old); e != nil || old.Diagnostics != nil {
		t.Fatal("legacy must remain unknown", e)
	}
}
