//go:build darwin || linux

package agents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/pelletier/go-toml/v2"
	"github.com/zwhu/memlane/internal/agentbridge"
	"github.com/zwhu/memlane/internal/records"
)

// Only initialize/thread-start/direct MCP calls are sent. There is no turn,
// login, model resolution, user config copy, chat read or upstream request.
func runCodexConnectionProtocol(ctx context.Context, d Discovery, i Installation, o RunOptions) (out Execution, resultErr error) {
	out = Execution{Agent: "codex", Version: d.Version, Events: map[string]int{}, Calls: []ToolCall{}}
	c := i.Connection
	if c.Agent != "codex" || d.Agent != "codex" || d.Path != i.Options.AgentPath || !i.Options.ComputerLevel || c.AutoProject || c.PendingProject != "" || c.TrustedWorkspace != "" || d.Version != i.AgentVersion {
		return out, errors.New("current computer-level Codex installation required")
	}
	if err := ComputerConnectionError(d); err != nil {
		return out, err
	}
	run, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	root, err := os.MkdirTemp("", "memlane-codex-connection-")
	if err != nil {
		return out, err
	}
	defer os.RemoveAll(root)
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return out, err
	}
	home, profile, work := filepath.Join(root, "home"), filepath.Join(root, "codex"), filepath.Join(root, "empty")
	for _, p := range []string{home, profile, work} {
		if err = os.Mkdir(p, 0700); err != nil {
			return out, err
		}
	}
	var workspaceDigest string
	if o.Workspace != "" {
		workspaceDigest, err = codexWorkspaceDigest(o.Workspace)
		if err != nil {
			return out, err
		}
		work, err = filepath.EvalSymlinks(o.Workspace)
		if err != nil {
			return out, err
		}
	}
	var config map[string]any
	if err = toml.Unmarshal([]byte(i.Block), &config); err != nil {
		return out, err
	}
	servers, ok := config["mcp_servers"].(map[string]any)
	if !ok {
		return out, errors.New("Codex owned MCP block missing")
	}
	server, ok := servers["memlane"].(map[string]any)
	if !ok {
		return out, errors.New("Codex owned MCP server missing")
	}
	args, ok := server["args"].([]any)
	if !ok {
		return out, errors.New("Codex owned arguments missing")
	}
	server["args"] = append(args, "--tool-mode", "connection", "--usage-channel", "verification")
	// These deliberately unusable settings prevent account/model discovery;
	// they are internal protocol fixtures, not a user model or a saved choice.
	config["model"], config["model_provider"] = "memlane-connection-only", "memlane_connection_only"
	config["model_providers"] = map[string]any{"memlane_connection_only": map[string]any{"name": "Local protocol only", "base_url": "http://127.0.0.1:9/v1", "wire_api": "responses", "requires_openai_auth": false}}
	config["analytics"] = map[string]any{"enabled": false}
	config["feedback"] = map[string]any{"enabled": false}
	data, err := toml.Marshal(config)
	if err != nil {
		return out, err
	}
	if err = os.WriteFile(filepath.Join(profile, "config.toml"), data, 0600); err != nil {
		return out, err
	}
	env := []string{"HOME=" + home, "CODEX_HOME=" + profile, "PATH=" + filepath.Dir(d.Path) + ":/usr/bin:/bin", "TMPDIR=" + root}
	rpc, err := startCodexRPC(run, d.Path, o.Guard, []string{"app-server", "--stdio"}, env, work, nil)
	if err != nil {
		return out, err
	}
	rpc.observe = func(message codexRPCMessage) {
		if message.Method == "turn/started" || message.Method == "item/started" {
			out.Events[message.Method]++
		}
	}
	defer func() {
		var process ProposalProcess
		closeErr := rpc.close(&process)
		out.PID, out.ProcessGroupGone = process.PID, process.GroupGone
		out.ExitCode = process.ExitCode
		if closeErr != nil {
			resultErr = errors.Join(resultErr, closeErr)
		}
		if resultErr != nil {
			out.Failure = safeReason(resultErr.Error())
		}
	}()
	if err = rpc.initialize(run); err != nil {
		return out, err
	}
	var started struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if err = rpc.call(run, "thread/start", map[string]any{"cwd": work, "ephemeral": true, "approvalPolicy": "never"}, &started); err != nil {
		return out, err
	}
	if started.Thread.ID == "" {
		return out, errors.New("Codex protocol thread missing")
	}
	var reply struct {
		Content []struct{ Type, Text string } `json:"content"`
		IsError bool                          `json:"isError"`
	}
	if err = rpc.call(run, "mcpServer/tool/call", map[string]any{"threadId": started.Thread.ID, "server": "memlane", "tool": "memory_scopes", "arguments": map[string]any{}}, &reply); err != nil {
		return out, err
	}
	if len(reply.Content) != 1 || reply.Content[0].Type != "text" {
		return out, errors.New("Codex scope handshake missing")
	}
	var receipt agentbridge.Result
	if err = json.Unmarshal([]byte(reply.Content[0].Text), &receipt); err != nil {
		return out, errors.New("Codex scope handshake malformed")
	}
	out.ProtocolReceipt = &receipt
	if o.Workspace != "" {
		after, e := codexWorkspaceDigest(o.Workspace)
		if e != nil || after != workspaceDigest {
			return out, errors.New("Codex verification workspace changed")
		}
	}
	if reply.IsError && receipt.OK {
		return out, errors.New("Codex scope handshake error envelope disagrees")
	}
	if out.Events["turn/started"] != 0 || out.Events["item/started"] != 0 {
		return out, errors.New("Codex connection unexpectedly started a turn or tool execution")
	}
	return out, nil
}

func codexWorkspaceDigest(workspace string) (string, error) {
	if !filepath.IsAbs(workspace) {
		return "", errors.New("absolute verified Codex workspace required")
	}
	canonical, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(canonical)
	if err != nil || !info.IsDir() {
		return "", errors.New("Codex workspace unavailable")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", errors.New("Codex workspace identity unavailable")
	}
	data, _ := json.Marshal([]any{canonical, stat.Dev, stat.Ino})
	return hash(data), nil
}

// CodexWorkspaceIdentity freezes a selected local directory, never a grant.
// Callers still resolve its registered project and check explicit authorization.
func CodexWorkspaceIdentity(workspace string) (string, error) { return codexWorkspaceDigest(workspace) }
func CodexProjectVerification(i Installation, project, workspace string, generation int64) bool {
	if _, ok := ConnectionVerification(i, generation); !ok || i.Connection.Agent != "codex" || !i.Options.ComputerLevel || project == "" || i.ProtocolProject != project || i.ProtocolWorkspaceDigest == "" {
		return false
	}
	allowed := false
	for _, scope := range i.Connection.AllowedScopes {
		if scope == project {
			allowed = true
		}
	}
	digest, err := codexWorkspaceDigest(workspace)
	return allowed && err == nil && digest == i.ProtocolWorkspaceDigest
}
func checkCodexProtocolTranscript(e Execution, i Installation, workspace string) (ProtocolVerificationContext, error) {
	var proof ProtocolVerificationContext
	if e.ProtocolReceipt == nil || e.ProtocolReceipt.Protocol != 1 || !e.ProtocolReceipt.OK || len(e.Calls) != 0 || e.Failure != "" || e.Events["turn/started"] != 0 || e.Events["item/started"] != 0 {
		if e.ProtocolReceipt != nil && e.ProtocolReceipt.Error != nil {
			return proof, fmt.Errorf("Codex connection protocol failed: %s", safeReason(e.ProtocolReceipt.Error.Code))
		}
		return proof, errors.New("Codex connection protocol unconfirmed")
	}
	var reply struct {
		Scopes   *[]records.Scope `json:"scopes"`
		Bound    *string          `json:"bound_project"`
		Current  *string          `json:"current_project"`
		Requires *bool            `json:"requires_scope"`
	}
	if json.Unmarshal(e.ProtocolReceipt.Result, &reply) != nil || reply.Scopes == nil || reply.Bound == nil || reply.Current == nil || reply.Requires == nil || *reply.Bound != i.Connection.Project || *reply.Requires != (*reply.Current == "") {
		return proof, errors.New("Codex connection scope receipt invalid")
	}
	expected := map[string]bool{}
	for _, id := range i.Connection.AllowedScopes {
		expected[id] = true
	}
	seen := map[string]bool{}
	hasProject := false
	currentAllowed := false
	for _, scope := range *reply.Scopes {
		if !expected[scope.ID] || seen[scope.ID] {
			return proof, errors.New("Codex connection scope set changed")
		}
		seen[scope.ID] = true
		if scope.Kind == "project" {
			hasProject = true
			if scope.ID == *reply.Current {
				currentAllowed = true
			}
		}
	}
	if len(expected) != len(seen) {
		return proof, errors.New("Codex connection scope set changed")
	}
	if workspace == "" {
		if hasProject || *reply.Current != "" {
			return proof, errors.New("select an explicitly authorized project workspace for connection verification")
		}
		return proof, nil
	}
	if !currentAllowed {
		return proof, errors.New("Codex verification workspace is not an authorized project")
	}
	digest, err := codexWorkspaceDigest(workspace)
	if err != nil {
		return proof, err
	}
	return ProtocolVerificationContext{Project: *reply.Current, WorkspaceDigest: digest}, nil
}
