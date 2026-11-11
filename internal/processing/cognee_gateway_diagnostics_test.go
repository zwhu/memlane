package processing

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zwhu/memlane/internal/records"
)

// Exercise the actual RPC -> completion -> gateway -> JSON receipt seam with
// local protocol fixtures. No Agent auth, model or running daemon is involved.
func TestCogneeGatewayRetainsActualRPCFailureDiagnostics(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	script, err := filepath.Abs("../agents/testdata/pi_diagnostics_rpc.py")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	program := filepath.Join(dir, "pi")
	var request struct {
		ResponseFormat struct {
			JSONSchema struct {
				Schema json.RawMessage `json:"schema"`
			} `json:"json_schema"`
		} `json:"response_format"`
	}
	if err = json.Unmarshal([]byte(cogneeGatewayRequest), &request); err != nil {
		t.Fatal(err)
	}
	// agents hash uses SHA256 over the exact RawMessage bytes.
	digest := completionTestHash(request.ResponseFormat.JSONSchema.Schema)
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
	code := "#!/bin/sh\ncase \"$1\" in --version) echo 0.84.1;exit 0;; --help) echo --extension;exit 0;; esac\nexec " + quote(python) + " " + quote(script) + " eof " + quote(digest) + " deepseek deepseek-flash\n"
	if err = os.WriteFile(program, []byte(code), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"defaultProvider":"deepseek","defaultModel":"deepseek-flash"}`), 0600); err != nil {
		t.Fatal(err)
	}
	o, g := cogneeGatewayFixture(t, nil)
	g.complete = o.completeCognee
	original := o.Resolve
	o.Resolve = func(ctx context.Context, a, b, c string, s records.ProcessingSpec) (PiBinding, error) {
		binding, e := original(ctx, a, b, c, s)
		binding.Executable = program
		binding.AuthHome = dir
		return binding, e
	}
	status, _ := cogneeGatewayPost(t, g, g.Token, cogneeGatewayRequest)
	if status != 502 {
		t.Fatal("failure fixture did not reach gateway", status)
	}
	receipts := g.Receipts()
	if len(receipts) != 1 || receipts[0].Process == nil || receipts[0].Success || receipts[0].Process.Terminal || !receipts[0].Process.Reaped || !receipts[0].Process.GroupGone || receipts[0].Process.SchemaResponseStatus != 200 {
		t.Fatal("wrong real failing seam", receipts)
	}
	if d := receipts[0].Diagnostics; d == nil || d.Stage != "completion" || d.FailureCode != "completion_failed" || d.RequestContext != "active" || d.BudgetContext != "active" || d.TaskContext != "active" {
		t.Fatalf("lost gateway context observations: %+v", d)
	}
	raw, err := json.Marshal(receipts[0])
	if err != nil {
		t.Fatal(err)
	}
	var serialized map[string]any
	if err = json.Unmarshal(raw, &serialized); err != nil {
		t.Fatal(err)
	}
	process := serialized["process"].(map[string]any)
	diagnostic, ok := process["diagnostics"].(map[string]any)
	if !ok || diagnostic["failure_code"] != "rpc_eof" {
		t.Fatal("actual RPC failure cause lost at gateway serializer", string(raw))
	}
}

func completionTestHash(raw []byte) string {
	value := sha256.Sum256(raw)
	return hex.EncodeToString(value[:])
}
