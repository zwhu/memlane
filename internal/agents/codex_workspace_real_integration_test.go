//go:build darwin || linux

package agents_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/zwhu/memlane/internal/agentbridge"
	"github.com/zwhu/memlane/internal/agents"
	"github.com/zwhu/memlane/internal/daemon"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
)

// Installed CLI owns each stdio cwd; the real bridge passes it to the real
// daemon. No prompt, turn, model, memory body or historical session is used.
func TestProtocolRealCodexGrantedWorkspaceIsolation(t *testing.T) {
	program, bridge, output := os.Getenv("MEMLANE_REAL_CODEX"), os.Getenv("MEMLANE_REAL_PROTOCOL_BRIDGE"), os.Getenv("MEMLANE_REAL_PROTOCOL_OUT")
	if program == "" || bridge == "" || output == "" {
		t.Skip("explicit isolated Codex/bridge/output required")
	}
	root, _ := filepath.EvalSymlinks(t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	serviceRoot := filepath.Join(root, "service")
	done := make(chan error, 1)
	go func() { done <- daemon.Run(ctx, daemon.Config{Root: serviceRoot}) }()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(10 * time.Second):
			t.Error("daemon cleanup timeout")
		}
	}()
	library := serviceipc.Library{Root: serviceRoot}
	for {
		if _, err := library.Scopes(ctx); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(20 * time.Millisecond):
		}
	}
	workspaces := []string{filepath.Join(root, "a"), filepath.Join(root, "b")}
	projects := []records.Scope{}
	for index, work := range workspaces {
		if err := exec.CommandContext(ctx, "/usr/bin/git", "init", "-q", work).Run(); err != nil {
			t.Fatal(err)
		}
		p, err := library.RegisterProject(ctx, records.ScopeDraft{Name: fmt.Sprintf("Generated workspace %d", index), Path: work})
		if err != nil {
			t.Fatal(err)
		}
		projects = append(projects, p)
	}
	c, err := library.ConfigureConnection(ctx, records.ConnectionDraft{Agent: "codex", Name: "Generated granted computer", AllowedScopes: []string{projects[0].ID}})
	if err != nil {
		t.Fatal(err)
	}
	profile := filepath.Join(root, "codex")
	home := filepath.Join(root, "home")
	for _, path := range []string{profile, home} {
		if err = os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	baseline := []byte("model = 'memlane-protocol-only'\nmodel_provider = 'local_disabled'\n[model_providers.local_disabled]\nname = 'No model'\nbase_url = 'http://127.0.0.1:9/v1'\nwire_api = 'responses'\nrequires_openai_auth = false\n[analytics]\nenabled = false\n")
	if err = os.WriteFile(filepath.Join(profile, "config.toml"), baseline, 0600); err != nil {
		t.Fatal(err)
	}
	i, err := agents.Install(ctx, c, agents.InstallOptions{ComputerLevel: true, ClientRoot: root, ServiceRoot: serviceRoot, Bridge: bridge, AgentPath: program, CodexDirectory: profile})
	if err != nil {
		t.Fatal(err)
	}
	defer agents.Uninstall(context.Background(), root, c.ID)
	probe := agents.Probe(ctx, "codex", program)
	if _, err = agents.VerifyProtocolConnection(ctx, probe, i, agents.RunOptions{}); err == nil {
		t.Fatal("project grants verified without explicit workspace")
	}
	if _, err = agents.VerifyProtocolConnection(ctx, probe, i, agents.RunOptions{Workspace: workspaces[1]}); err == nil {
		t.Fatal("ungranted verification workspace accepted")
	}
	if _, err = agents.VerifyProtocolConnection(ctx, probe, i, agents.RunOptions{Workspace: workspaces[0]}); err != nil {
		t.Fatal(err)
	}
	verified, err := agents.LoadInstallation(root, c.ID)
	if err != nil || !agents.CodexProjectVerification(verified, projects[0].ID, workspaces[0], c.Generation) || agents.CodexProjectVerification(verified, projects[1].ID, workspaces[1], c.Generation) {
		t.Fatal("project A proof claimed project B", err)
	}
	command := exec.CommandContext(ctx, program, "app-server", "--stdio")
	command.Dir = root
	command.Env = []string{"HOME=" + home, "CODEX_HOME=" + profile, "PATH=" + filepath.Dir(program) + ":/usr/bin:/bin", "TMPDIR=" + root}
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Stderr = io.Discard
	input, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		input.Close()
		syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		stdout.Close()
		command.Wait()
	}()
	encoder, decoder := json.NewEncoder(input), json.NewDecoder(stdout)
	serial := 0
	send := func(method string, params any) int {
		serial++
		if err := encoder.Encode(map[string]any{"id": serial, "method": method, "params": params}); err != nil {
			t.Fatal(err)
		}
		return serial
	}
	read := func() map[string]json.RawMessage {
		var msg map[string]json.RawMessage
		if err := decoder.Decode(&msg); err != nil {
			t.Fatal(err)
		}
		return msg
	}
	wait := func(id int, result any) {
		for {
			msg := read()
			var got int
			if json.Unmarshal(msg["id"], &got) != nil || got != id {
				continue
			}
			if len(msg["error"]) != 0 {
				t.Fatal("generated Codex request rejected")
			}
			if err := json.Unmarshal(msg["result"], result); err != nil {
				t.Fatal(err)
			}
			return
		}
	}
	var initialized map[string]any
	wait(send("initialize", map[string]any{"clientInfo": map[string]string{"name": "memlane_context_acceptance", "version": "1"}, "capabilities": map[string]bool{"experimentalApi": true}}), &initialized)
	if err = encoder.Encode(map[string]any{"method": "initialized"}); err != nil {
		t.Fatal(err)
	}
	threads := []string{}
	for _, work := range workspaces {
		var started struct {
			Thread struct {
				ID string `json:"id"`
			} `json:"thread"`
		}
		wait(send("thread/start", map[string]any{"cwd": work, "ephemeral": true, "approvalPolicy": "never"}), &started)
		if started.Thread.ID == "" {
			t.Fatal("thread missing")
		}
		threads = append(threads, started.Thread.ID)
	}
	params := func(thread, tool string, args any) map[string]any {
		return map[string]any{"threadId": thread, "server": "memlane", "tool": tool, "arguments": args, "_meta": map[string]any{"cwd": "/untrusted-spoof", "workspace": workspaces[0]}}
	}
	decodeReceipt := func(raw json.RawMessage) agentbridge.Result {
		var reply struct {
			Content []struct{ Type, Text string } `json:"content"`
		}
		if json.Unmarshal(raw, &reply) != nil || len(reply.Content) != 1 {
			t.Fatal("bridge receipt missing")
		}
		var result agentbridge.Result
		if json.Unmarshal([]byte(reply.Content[0].Text), &result) != nil {
			t.Fatal("bridge receipt invalid")
		}
		return result
	}
	pending := map[int]int{}
	for index, thread := range threads {
		pending[send("mcpServer/tool/call", params(thread, "memory_scopes", map[string]any{}))] = index
	}
	for len(pending) > 0 {
		msg := read()
		var id int
		if json.Unmarshal(msg["id"], &id) != nil {
			continue
		}
		index, ok := pending[id]
		if !ok {
			continue
		}
		delete(pending, id)
		receipt := decodeReceipt(msg["result"])
		var scope struct {
			Current string `json:"current_project"`
		}
		if !receipt.OK || json.Unmarshal(receipt.Result, &scope) != nil || scope.Current != projects[index].ID {
			t.Fatal("workspace crossed threads", index, receipt)
		}
	}
	get := func(thread, tool string, args any) agentbridge.Result {
		var raw json.RawMessage
		wait(send("mcpServer/tool/call", params(thread, tool, args)), &raw)
		return decodeReceipt(raw)
	}
	blocked := get(threads[1], "memory_status", map[string]any{"memory_id": "00000000-0000-4000-8000-000000000001"})
	if blocked.Error == nil || blocked.Error.Code != "scope_denied" {
		t.Fatal("ungranted current workspace was accepted", blocked)
	}
	blocked = get(threads[0], "memory_status", map[string]any{"scope": projects[1].ID, "memory_id": "00000000-0000-4000-8000-000000000001"})
	if blocked.Error == nil || blocked.Error.Code != "scope_denied" {
		t.Fatal("explicit foreign scope was accepted", blocked)
	}
	next, err := library.ConfigureConnection(ctx, records.ConnectionDraft{ID: c.ID, Agent: "codex", Name: c.Name, AllowedScopes: []string{projects[0].ID, projects[1].ID}})
	if err != nil {
		t.Fatal(err)
	}
	stale := get(threads[0], "memory_scopes", map[string]any{})
	if stale.Error == nil || stale.Error.Code != "reload_required" {
		t.Fatal("old process generation remained usable", stale)
	}
	if _, ok := agents.ConnectionVerification(verified, next.Generation); ok {
		t.Fatal("authorization generation retained old proof")
	}
	reloaded, err := agents.Install(ctx, next, i.Options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = agents.VerifyProtocolConnection(ctx, probe, reloaded, agents.RunOptions{Workspace: workspaces[1]}); err != nil {
		t.Fatal("reload did not permit current authorized protocol verification", err)
	}
	verified, err = agents.LoadInstallation(root, c.ID)
	if err != nil || !agents.CodexProjectVerification(verified, projects[1].ID, workspaces[1], next.Generation) || agents.CodexProjectVerification(verified, projects[0].ID, workspaces[0], next.Generation) {
		t.Fatal("new B proof was reused for A", err)
	}
	report := map[string]any{"passed": !t.Failed(), "version": i.AgentVersion, "real_model_requests": 0, "app_server_turns_started": 0, "private_thread_current_projects": true, "request_meta_cwd_ignored": true, "ungranted_current_scope": "scope_denied", "explicit_foreign_scope": "scope_denied", "changed_grants": "reload_required", "user_configuration_modified": false}
	report["granted_reload_protocol_lifecycle"] = true
	report["project_a_proof_not_reused_for_b"] = true
	report["project_b_proof_not_reused_for_a"] = true
	data, _ := json.MarshalIndent(report, "", "  ")
	if err = os.WriteFile(filepath.Join(output, "codex-scope-isolation-"+time.Now().UTC().Format("150405.000000000")+".json"), data, 0600); err != nil {
		t.Fatal(err)
	}
}
