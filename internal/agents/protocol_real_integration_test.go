package agents_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/zwhu/memlane/internal/agents"
	"github.com/zwhu/memlane/internal/daemon"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
	"github.com/zwhu/memlane/internal/store"
)

// Opt-in real installed pi + CLI + daemon. No auth links, model calls, user
// configuration changes or components are needed for a protocol handshake.
func TestProtocolRealPiWithoutModel(t *testing.T) {
	bridge, output := os.Getenv("MEMLANE_REAL_PROTOCOL_BRIDGE"), os.Getenv("MEMLANE_REAL_PROTOCOL_OUT")
	if bridge == "" || output == "" {
		t.Skip("explicit isolated protocol output and bridge required")
	}
	if !filepath.IsAbs(bridge) || !filepath.IsAbs(output) {
		t.Fatal("absolute paths required")
	}
	if err := os.MkdirAll(output, 0700); err != nil {
		t.Fatal(err)
	}
	root, err := os.MkdirTemp(output, "protocol-")
	if err != nil {
		t.Fatal(err)
	}
	report := map[string]any{"root": root, "model_inference": false, "user_configuration_modified": false}
	defer func() {
		report["passed"] = !t.Failed()
		data, e := json.MarshalIndent(report, "", "  ")
		if e != nil {
			t.Error(e)
			return
		}
		if e = os.WriteFile(filepath.Join(root, "report.json"), data, 0600); e != nil {
			t.Error(e)
		}
		t.Log("protocol evidence:", filepath.Join(root, "report.json"))
	}()
	program, err := exec.LookPath("pi")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	serviceRoot := filepath.Join(root, "service")
	done := make(chan error, 1)
	stopService := cancel
	killService := func() {}
	if helper := os.Getenv("MEMLANE_REAL_PROTOCOL_HELPER"); helper != "" {
		if !filepath.IsAbs(helper) {
			t.Fatal("absolute packaged helper required")
		}
		config, _ := json.Marshal(daemon.Config{Root: serviceRoot})
		configPath := filepath.Join(root, "service-config.json")
		if err = os.WriteFile(configPath, config, 0600); err != nil {
			t.Fatal(err)
		}
		log, e := os.OpenFile(filepath.Join(root, "helper.log"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			t.Fatal(e)
		}
		defer log.Close()
		command := exec.Command(helper, "--config", configPath)
		command.Stdout, command.Stderr = log, log
		if err = command.Start(); err != nil {
			t.Fatal(err)
		}
		report["packaged_helper"], report["helper_pid"] = helper, command.Process.Pid
		go func() { done <- command.Wait() }()
		stopService = func() { _ = command.Process.Signal(os.Interrupt) }
		killService = func() { _ = command.Process.Kill() }
	} else {
		go func() { done <- daemon.Run(ctx, daemon.Config{Root: serviceRoot}) }()
	}
	stopped := false
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		stopService()
		select {
		case e := <-done:
			if e != nil {
				t.Error("daemon cleanup", e)
			}
			report["daemon_stopped"] = e == nil
		case <-time.After(10 * time.Second):
			t.Error("daemon cleanup timeout")
			killService()
			select {
			case <-done:
				report["daemon_force_stopped"] = true
			case <-time.After(5 * time.Second):
				report["daemon_force_stopped"] = false
			}
		}
	}
	defer stop()
	library := serviceipc.Library{Root: serviceRoot}
	for {
		if _, e := library.Scopes(ctx); e == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("daemon not ready", ctx.Err())
		case <-time.After(40 * time.Millisecond):
		}
	}
	c, err := library.ConfigureConnection(ctx, records.ConnectionDraft{Agent: "pi", Name: "Real pi protocol only", ConnectionOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	d := agents.Probe(ctx, "pi", program)
	report["discovery"] = d
	i, err := agents.Install(ctx, c, agents.InstallOptions{ClientRoot: root, ServiceRoot: serviceRoot, Bridge: bridge, AgentPath: program, PiDirectory: filepath.Join(root, "pi")})
	if err != nil {
		t.Fatal(err)
	}
	e, err := agents.VerifyProtocolConnection(ctx, d, i, agents.RunOptions{})
	report["execution"] = e
	if err != nil {
		report["error"] = err.Error()
		t.Fatal(err)
	}
	if e.Events["agent_start"] != 0 || e.Events["turn_start"] != 0 || len(e.Calls) != 0 || !e.ProcessGroupGone {
		t.Fatal("unexpected model/tool activity or surviving process", e)
	}
	saved, err := agents.LoadInstallation(root, c.ID)
	if err != nil || saved.ProtocolGeneration != c.Generation || saved.VerifiedGeneration != 0 {
		t.Fatal("receipt not independently persisted", err)
	}
	report["protocol_generation"] = saved.ProtocolGeneration
	if err = library.Disconnect(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	failed, failure := agents.VerifyProtocolConnection(ctx, d, i, agents.RunOptions{})
	report["revoked_execution"] = failed
	if failure == nil {
		t.Fatal("revoked identity passed protocol verification")
	}
	if failed.ProtocolReceipt == nil || failed.ProtocolReceipt.Error == nil || failed.ProtocolReceipt.Error.Code != "disconnected" {
		t.Fatal("revalidation did not prove daemon-side revocation", failed.ProtocolReceipt, failure)
	}
	report["revoked_error"] = failure.Error()
	saved, err = agents.LoadInstallation(root, c.ID)
	if err != nil || saved.ProtocolGeneration != 0 || saved.ProtocolVerifiedAt != "" || saved.ProtocolError == "" {
		t.Fatal("failed revalidation retained protocol success", err)
	}
	if !failed.ProcessGroupGone || failed.Events["agent_start"] != 0 {
		t.Fatal("failed protocol verification leaked a process or invoked a model")
	}
	if err = agents.Uninstall(ctx, root, c.ID); err != nil {
		t.Fatal(err)
	}
	stop()
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
	report["memory_count"], report["project_count"] = memories, projects
	if memories != 0 || projects != 0 {
		t.Fatal("connection verification created business data", memories, projects)
	}
}
