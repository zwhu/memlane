//go:build darwin || linux

package agents

// Process/protocol integration fixtures. They are not model calls and never
// contribute to the real-Codex acceptance result.
import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/engine"
	"github.com/zwhu/memlane/internal/records"
)

func fixtureSnapshot() (records.P3Snapshot, []byte) {
	x := records.P3Snapshot{Schema: records.P3ExperimentSchema, Task: uuid.NewString(), Attempt: 1, Dataset: uuid.NewString()}
	for _, text := range []string{"Synthetic pnpm policy.", "Synthetic lockfile policy."} {
		x.Inputs = append(x.Inputs, records.Version{Record: engine.Record{Memory: uuid.NewString(), Dataset: x.Dataset, Revision: 1, Text: text}, State: "active"})
	}
	if err := x.Seal(); err != nil {
		panic(err)
	}
	p := records.P3Proposal{Schema: x.Schema, Task: x.Task, Attempt: 1, Digest: x.Digest, ID: x.ProposalID(), Action: "propose_merge", Title: "Synthetic merge", Text: "Synthetic pnpm and lockfile policy."}
	for _, v := range x.Inputs {
		p.Sources = append(p.Sources, records.P3Source{Memory: v.Memory, Revision: 1, Quote: v.Text})
	}
	b, _ := json.Marshal(p)
	return x, b
}

func TestP3ProtocolFixtures(t *testing.T) {
	for _, mode := range []string{"normal", "no_result", "invalid_json", "missing_terminal", "terminal_before_result", "failed_exit_zero", "item_error", "tool_call", "late_after_cancel", "descendant"} {
		t.Run(mode, func(t *testing.T) {
			x, b := fixtureSnapshot()
			dir := t.TempDir()
			env := []string{"MEMLANE_P3_FIXTURE=" + mode, "MEMLANE_P3_PAYLOAD=" + base64.StdEncoding.EncodeToString(b), "MEMLANE_P3_CHILD_FILE=" + filepath.Join(dir, "child-pid")}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var hook func(string)
			if mode == "late_after_cancel" || mode == "descendant" {
				hook = func(event string) {
					if event == "turn.started" {
						cancel()
					}
				}
			}
			start := time.Now()
			out, err := runP3Process(ctx, os.Args[0], []string{"-test.run=^TestP3ProtocolProcessFixture$"}, env, dir, "", x, hook)
			if !out.Started || !out.Reaped || !out.GroupGone {
				t.Fatalf("process not closed: %+v", out)
			}
			if mode == "normal" {
				if err != nil || out.Proposal == nil {
					t.Fatalf("valid fixture rejected: %v %+v", err, out)
				}
			} else {
				if err == nil || out.Proposal != nil {
					t.Fatalf("invalid/late fixture released result: %+v", out)
				}
			}
			if mode == "late_after_cancel" || mode == "descendant" {
				if !out.Cancelled || time.Since(start) > 3*time.Second {
					t.Fatal("cancel did not close promptly")
				}
			}
			if mode == "descendant" {
				data, err := os.ReadFile(filepath.Join(dir, "child-pid"))
				if err != nil {
					t.Fatal(err)
				}
				pid, err := strconv.Atoi(string(data))
				if err != nil {
					t.Fatal(err)
				}
				if syscall.Kill(pid, 0) != syscall.ESRCH {
					t.Fatal("child survived cancellation")
				}
			}
		})
	}
}

// This subprocess is intentionally controlled and has no model or auth.
func TestP3ProtocolProcessFixture(t *testing.T) {
	mode := os.Getenv("MEMLANE_P3_FIXTURE")
	if mode == "" {
		t.Skip("subprocess fixture only")
	}
	if mode == "child" {
		for {
			time.Sleep(time.Second)
		}
	}
	payload, _ := base64.StdEncoding.DecodeString(os.Getenv("MEMLANE_P3_PAYLOAD"))
	emit := func(v any) {
		if err := json.NewEncoder(os.Stdout).Encode(v); err != nil {
			os.Exit(2)
		}
	}
	if mode == "descendant" {
		child := exec.Command(os.Args[0], "-test.run=^TestP3ProtocolProcessFixture$")
		child.Env = []string{"MEMLANE_P3_FIXTURE=child"}
		child.Stdout = os.Stdout
		child.Stderr = os.Stderr
		if child.Start() != nil {
			os.Exit(3)
		}
		if os.WriteFile(os.Getenv("MEMLANE_P3_CHILD_FILE"), []byte(strconv.Itoa(child.Process.Pid)), 0600) != nil {
			os.Exit(4)
		}
	}
	emit(map[string]any{"type": "thread.started"})
	emit(map[string]any{"type": "turn.started"})
	if mode == "descendant" {
		for {
			time.Sleep(time.Second)
		}
	}
	if mode == "failed_exit_zero" {
		emit(map[string]any{"type": "turn.failed", "error": map[string]any{"message": "synthetic quota failure"}})
		os.Exit(0)
	}
	if mode == "tool_call" {
		emit(map[string]any{"type": "item.completed", "item": map[string]any{"type": "mcp_tool_call"}})
	}
	if mode == "item_error" {
		emit(map[string]any{"type": "item.completed", "item": map[string]any{"type": "error", "message": "synthetic initialization error"}})
	}
	if mode == "invalid_json" {
		payload = []byte("not json")
	}
	if mode == "terminal_before_result" {
		emit(map[string]any{"type": "turn.completed"})
	}
	if mode != "no_result" {
		emit(map[string]any{"type": "item.completed", "item": map[string]any{"type": "agent_message", "text": string(payload)}})
	}
	if mode != "missing_terminal" && mode != "terminal_before_result" {
		emit(map[string]any{"type": "turn.completed"})
	}
	// Buffered output above may be read after the parent has already cancelled.
	if mode == "late_after_cancel" {
		fmt.Fprint(os.Stdout, "")
	}
	os.Exit(0)
}
