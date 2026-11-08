package official

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/serviceipc"
)

// Format/ownership regression only; the real Codex task log is validated in
// the packaged App separately.
func TestTranscriptPreservesLongToolOutputAndRejectsWrongWorkspace(t *testing.T) {
	c := Controller{Root: t.TempDir()}
	project, session := uuid.NewString(), uuid.NewString()
	root, _ := c.agentRoot(project)
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	scope := AgentScope{Project: project, Workspace: t.TempDir(), Home: filepath.Join(root, "home")}
	if err := serviceipc.WriteJSON(filepath.Join(root, "scope.json"), scope); err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(scope.Home, ".codex/sessions/2026/11/08")
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	long := strings.Repeat("实际工具返回 α\n", 1600)
	rows := []map[string]any{
		{"type": "session_meta", "timestamp": "2026-11-08T00:00:00Z", "payload": map[string]any{"id": session, "cwd": scope.Workspace, "source": "exec", "cli_version": "0.167.0-alpha.3.2"}},
		{"type": "response_item", "payload": map[string]any{"type": "message", "role": "developer", "content": []map[string]string{{"type": "input_text", "text": "internal instruction excluded"}}}},
		{"type": "response_item", "payload": map[string]any{"type": "message", "role": "user", "content": []map[string]string{{"type": "input_text", "text": "actual user request"}}}},
		{"type": "response_item", "payload": map[string]any{"type": "custom_tool_call", "name": "exec", "call_id": "real-call", "input": "actual tool request"}},
		{"type": "response_item", "payload": map[string]any{"type": "custom_tool_call_output", "call_id": "real-call", "output": []map[string]string{{"type": "input_text", "text": long}}}},
		{"type": "response_item", "payload": map[string]any{"type": "message", "role": "assistant", "phase": "final_answer", "content": []map[string]string{{"type": "output_text", "text": "actual final answer"}}}},
		{"type": "event_msg", "payload": map[string]string{"type": "task_complete"}},
	}
	var file strings.Builder
	for _, row := range rows {
		raw, _ := json.Marshal(row)
		file.Write(raw)
		file.WriteByte('\n')
	}
	path := filepath.Join(directory, "rollout-synthetic-"+session+".jsonl")
	if err := os.WriteFile(path, []byte(file.String()), 0600); err != nil {
		t.Fatal(err)
	}
	body, original, err := c.Transcript(project, "codex_"+session, scope.Workspace)
	if err != nil || !strings.Contains(body, long) || strings.Contains(body, "internal instruction excluded") || original.Digest == "" {
		t.Fatalf("lost original: err=%v", err)
	}
	if _, _, err = c.Transcript(project, "codex_"+session, t.TempDir()); err == nil {
		t.Fatal("wrong workspace accepted")
	}
	if err = os.WriteFile(path, []byte(strings.Replace(file.String(), `"task_complete"`, `"task_started"`, 1)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err = c.Transcript(project, "codex_"+session, scope.Workspace); err == nil {
		t.Fatal("running task counted as complete")
	}
}
