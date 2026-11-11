package agents_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/agents"
	"github.com/zwhu/memlane/internal/daemon"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
	"github.com/zwhu/memlane/internal/store"
)

func startBatchHelper(t *testing.T, helper, configPath, logPath string) func() {
	t.Helper()
	log, err := os.OpenFile(logPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(helper, "--config", configPath)
	cmd.Stdout, cmd.Stderr = log, log
	if err = cmd.Start(); err != nil {
		log.Close()
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait(); log.Close() }()
	stopped := false
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		_ = cmd.Process.Signal(os.Interrupt)
		select {
		case err := <-done:
			if err != nil {
				t.Error("packaged daemon exit", err)
			}
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()
			<-done
			t.Error("packaged daemon required forced exit")
		}
	}
	t.Cleanup(stop)
	return stop
}

// Actual package helper/CLI/pi with a clearly identified filesystem fault.
// The unsupported TRAE CLI target is not passed off as an installed connection.
func TestAgentBatchRealPiRetryAndDaemonRestart(t *testing.T) {
	helper, output := os.Getenv("MEMLANE_REAL_PROTOCOL_HELPER"), os.Getenv("MEMLANE_REAL_PROTOCOL_OUT")
	if helper == "" || output == "" {
		t.Skip("explicit packaged helper and isolated output required")
	}
	if !filepath.IsAbs(helper) || !filepath.IsAbs(output) {
		t.Fatal("absolute paths required")
	}
	if err := os.MkdirAll(output, 0700); err != nil {
		t.Fatal(err)
	}
	root, err := os.MkdirTemp(output, "batch-")
	if err != nil {
		t.Fatal(err)
	}
	report := map[string]any{"root": root, "packaged_helper": helper, "synthetic_only": true, "filesystem_fault": "regular file blocks isolated pi configuration directory"}
	defer func() {
		report["passed"] = !t.Failed()
		data, _ := json.MarshalIndent(report, "", "  ")
		if e := os.WriteFile(filepath.Join(root, "report.json"), data, 0600); e != nil {
			t.Error(e)
		}
		t.Log("batch evidence:", filepath.Join(root, "report.json"))
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	serviceRoot := filepath.Join(root, "data")
	configPath := filepath.Join(root, "service-config.json")
	config, _ := json.Marshal(daemon.Config{Root: serviceRoot, ClientRoot: root})
	if err = os.WriteFile(configPath, config, 0600); err != nil {
		t.Fatal(err)
	}
	stop := startBatchHelper(t, helper, configPath, filepath.Join(root, "helper-1.log"))
	defer stop()
	library := serviceipc.Library{Root: serviceRoot}
	waitState := func(predicate func(records.AgentConnectionBatchState) bool) records.AgentConnectionBatchState {
		t.Helper()
		for {
			state, e := library.AgentConnectionBatch(ctx, "")
			if e == nil && predicate(state) {
				return state
			}
			select {
			case <-ctx.Done():
				t.Fatal("batch state timeout", ctx.Err(), e)
			case <-time.After(30 * time.Millisecond):
			}
		}
	}
	state := waitState(func(s records.AgentConnectionBatchState) bool { return s.Identity != "" })
	pi, err := exec.LookPath("pi")
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(root, "pi")
	if err = os.WriteFile(directory, []byte("synthetic installation blocker"), 0600); err != nil {
		t.Fatal(err)
	}
	d := records.AgentConnectionBatchStart{Identity: state.Identity, OperationID: uuid.NewString(), Targets: []records.AgentConnectionTarget{{Agent: "pi", Path: pi, Directory: directory}, {Agent: "traex", Path: "/synthetic/unsupported-traex", Directory: filepath.Join(root, "traex")}}}
	b, err := library.StartAgentConnectionBatch(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	state = waitState(func(s records.AgentConnectionBatchState) bool {
		return s.Batch != nil && s.Batch.State == "partial_failed"
	})
	report["failed_installation"] = state.Batch
	if state.Batch.Items[0].State != "failed" || state.Batch.Items[1].State != "unsupported" {
		t.Fatal("filesystem failure or unsupported adapter hidden", state.Batch)
	}
	connectionID := state.Batch.Items[0].ConnectionID
	if err = os.Remove(directory); err != nil {
		t.Fatal(err)
	}
	retry := records.AgentConnectionBatchAction{Identity: state.Identity, BatchID: b.ID, OperationID: uuid.NewString(), Kind: "retry"}
	if _, err = library.ActAgentConnectionBatch(ctx, retry); err != nil {
		t.Fatal(err)
	}
	state = waitState(func(s records.AgentConnectionBatchState) bool { return s.Batch != nil && s.Batch.State == "waiting" })
	report["installed"] = state.Batch
	if state.Batch.Items[0].ConnectionID != connectionID || state.Batch.Items[0].Generation != 1 || state.Batch.Items[1].Attempt != 1 {
		t.Fatal("retry duplicated identity or retried unsupported target", state.Batch)
	}
	before, err := os.ReadFile(filepath.Join(directory, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	stop()
	stop2 := startBatchHelper(t, helper, configPath, filepath.Join(root, "helper-2.log"))
	defer stop2()
	state = waitState(func(s records.AgentConnectionBatchState) bool { return s.Batch != nil && s.Batch.State == "waiting" })
	after, err := os.ReadFile(filepath.Join(directory, "settings.json"))
	if err != nil || string(before) != string(after) || state.Batch.ID != b.ID {
		t.Fatal("restart reinstalled or replaced the batch", err)
	}
	verify := records.AgentConnectionBatchAction{Identity: state.Identity, BatchID: b.ID, OperationID: uuid.NewString(), Kind: "verify"}
	if _, err = library.ActAgentConnectionBatch(ctx, verify); err != nil {
		t.Fatal(err)
	}
	state = waitState(func(s records.AgentConnectionBatchState) bool {
		return s.Batch != nil && s.Batch.State == "partial_failed" && s.Batch.Items[0].State == "connected"
	})
	report["verified"] = state.Batch
	var execution agents.Execution
	if err = json.Unmarshal(state.Batch.Items[0].Verification, &execution); err != nil || !execution.ProcessGroupGone || execution.ProtocolReceipt == nil || !execution.ProtocolReceipt.OK || execution.Events["agent_start"] != 0 {
		t.Fatal("missing guarded no-model verification", err, execution)
	}
	attempt := state.Batch.Items[0].Attempt
	replayed, err := library.ActAgentConnectionBatch(ctx, verify)
	if err != nil || replayed.Items[0].Attempt != attempt {
		t.Fatal("verification replay started another attempt", err)
	}
	connections, err := library.Connections(ctx)
	if err != nil || len(connections) != 1 || len(connections[0].AllowedScopes) != 0 {
		t.Fatal("batch implicitly granted access", err, connections)
	}
	if err = agents.Uninstall(ctx, root, connectionID); err != nil {
		t.Fatal(err)
	}
	if err = library.Disconnect(ctx, connectionID); err != nil {
		t.Fatal(err)
	}
	stop2()
	db, err := store.Open(filepath.Join(serviceRoot, "memlane.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var memories, projects int
	if err = db.DB.QueryRow(`SELECT count(*) FROM memories`).Scan(&memories); err != nil {
		t.Fatal(err)
	}
	if err = db.DB.QueryRow(`SELECT count(*) FROM scopes WHERE kind='project'`).Scan(&projects); err != nil {
		t.Fatal(err)
	}
	report["memory_count"], report["project_count"], report["socket_absent"] = memories, projects, !fileExists(serviceipc.SocketPath(serviceRoot))
	if memories != 0 || projects != 0 {
		t.Fatal("batch produced business data")
	}
}

func fileExists(path string) bool { _, err := os.Stat(path); return err == nil }
