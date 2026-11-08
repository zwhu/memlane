// p3-pi-rpc-check runs the actual pi runner on explicitly synthetic snapshots.
// It is not bundled; it proves neither App execution nor Rust indexing.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/agents"
	"github.com/zwhu/memlane/internal/records"
)

func main() { os.Exit(run()) }
func run() int {
	mode := flag.String("mode", "normal", "normal or active cancellation")
	executable := flag.String("executable", "", "actual pi absolute executable")
	authHome := flag.String("auth-home", "", "explicit isolated validation pi configuration")
	flag.Parse()
	if *mode != "normal" && *mode != "cancel" {
		return 2
	}
	task, project, source := uuid.NewString(), uuid.NewString(), uuid.NewString()
	snapshot := records.OrganizationSnapshot{Schema: records.OrganizationSchema, Task: task, Project: project, Attempt: 1, Inputs: []records.OrganizationInput{{MergeInput: records.MergeInput{Memory: source, Revision: 1, Text: "本合成项目使用 pnpm 管理依赖；依赖变化时提交 pnpm-lock.yaml。", Title: "合成依赖约定", Source: "synthetic", CapturedAt: time.Now().UTC().Format(time.RFC3339)}, Origin: "knowledge", Roots: []records.MergeTarget{{Memory: source, Revision: 1}}}}}
	if err := snapshot.Seal(); err != nil {
		panic(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := time.Now()
	activeCancelTriggered := false
	out, err := agents.RunPiOrganization(ctx, agents.PiOrganizationRunOptions{Executable: *executable, AuthHome: *authHome, Snapshot: snapshot, OnEvent: func(event string) {
		if *mode == "cancel" && event == "message_update" {
			activeCancelTriggered = true
			cancel()
		}
	}})
	passed := err == nil && out.Result != nil && out.Terminal && out.Reaped && out.GroupGone && !out.Cancelled
	if *mode == "cancel" {
		passed = activeCancelTriggered && errors.Is(err, context.Canceled) && out.Events["agent_settled"] == 1 && out.Result == nil && out.Accepted && out.Cancelled && out.AbortSent && out.AbortAcknowledged && out.Reaped && out.GroupGone
	}
	status := "failed"
	if passed {
		status = "passed"
	}
	report := map[string]any{"active_message_update_cancel_triggered": activeCancelTriggered, "status": status, "mode": *mode, "synthetic_only": true, "actual_pi_runner_attempted": true, "model_protocol_fixture": false, "actual_app": false, "actual_rust": false, "actual_store_application": false, "execution": out, "seconds": time.Since(started).Seconds()}
	if err != nil {
		report["error"] = err.Error()
	}
	if out.TransportCandidate != "" {
		report["synthetic_unvalidated_transport"] = out.TransportCandidate
	}
	if out.ProcessDiagnostic != "" {
		report["synthetic_process_diagnostic"] = out.ProcessDiagnostic
	}
	_ = json.NewEncoder(os.Stdout).Encode(report)
	if !passed {
		return 1
	}
	return 0
}
