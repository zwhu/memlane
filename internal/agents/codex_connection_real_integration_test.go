package agents_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zwhu/memlane/internal/agents"
	"github.com/zwhu/memlane/internal/daemon"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
	"github.com/zwhu/memlane/internal/store"
)

// Explicitly opt-in: installed Codex + source CLI bridge + real daemon/SQLite.
// The only installed configuration belongs to a new temporary CODEX_HOME.
func TestProtocolRealCodexWithoutModel(t *testing.T) {
	program, bridge, output := os.Getenv("MEMLANE_REAL_CODEX"), os.Getenv("MEMLANE_REAL_PROTOCOL_BRIDGE"), os.Getenv("MEMLANE_REAL_PROTOCOL_OUT")
	if program == "" || bridge == "" || output == "" {
		t.Skip("explicit isolated Codex/bridge/output required")
	}
	for _, p := range []string{program, bridge, output} {
		if !filepath.IsAbs(p) {
			t.Fatal("absolute paths required")
		}
	}
	if err := os.MkdirAll(output, 0700); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	report := map[string]any{"real_model_requests": 0, "user_configuration_modified": false, "packaged_native_acceptance": false}
	defer func() {
		report["passed"] = !t.Failed()
		data, _ := json.MarshalIndent(report, "", "  ")
		name := filepath.Join(output, "codex-empty-grant-"+time.Now().UTC().Format("150405.000000000")+".json")
		if err := os.WriteFile(name, data, 0600); err != nil {
			t.Error(err)
		}
		t.Log(name)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	serviceRoot := filepath.Join(root, "service")
	done := make(chan error, 1)
	go func() { done <- daemon.Run(ctx, daemon.Config{Root: serviceRoot}) }()
	stopped := false
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(10 * time.Second):
			t.Error("daemon cleanup timeout")
		}
	}
	defer stop()
	library := serviceipc.Library{Root: serviceRoot}
	for {
		if _, err := library.Scopes(ctx); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(30 * time.Millisecond):
		}
	}
	c, err := library.ConfigureConnection(ctx, records.ConnectionDraft{Agent: "codex", Name: "Isolated Codex protocol", ConnectionOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	d := agents.Probe(ctx, "codex", program)
	report["discovery"] = d
	i, err := agents.Install(ctx, c, agents.InstallOptions{ComputerLevel: true, ClientRoot: root, ServiceRoot: serviceRoot, Bridge: bridge, AgentPath: program, CodexDirectory: filepath.Join(root, "isolated-codex")})
	if err != nil {
		t.Fatal(err)
	}
	execution, err := agents.VerifyProtocolConnection(ctx, d, i, agents.RunOptions{})
	report["execution"] = execution
	if err != nil {
		report["error"] = err.Error()
		t.Fatal(err)
	}
	if !execution.ProcessGroupGone || len(execution.Calls) != 0 || execution.Events["turn/started"] != 0 || execution.Events["item/started"] != 0 {
		t.Fatal("protocol isolation failed", execution)
	}
	saved, err := agents.LoadInstallation(root, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := agents.ConnectionVerification(saved, c.Generation); !ok || saved.VerifiedGeneration != 0 {
		t.Fatal("independent protocol receipt missing")
	}
	if err = library.Disconnect(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	failed, failure := agents.VerifyProtocolConnection(ctx, d, i, agents.RunOptions{})
	report["revoked_execution"] = failed
	if failure == nil || failed.ProtocolReceipt == nil || failed.ProtocolReceipt.Error == nil || failed.ProtocolReceipt.Error.Code != "disconnected" || !failed.ProcessGroupGone {
		t.Fatal("daemon revocation not proven", failed, failure)
	}
	saved, err = agents.LoadInstallation(root, c.ID)
	if err != nil || saved.ProtocolGeneration != 0 || saved.ProtocolError == "" {
		t.Fatal("revocation retained success", err)
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
	report["memories"], report["projects"] = memories, projects
	if memories != 0 || projects != 0 {
		t.Fatal("connection created business data")
	}
}
