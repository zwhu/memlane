package agents

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

// Transcript fixtures verify file attribution and version fences, not a model.
func historyLogFixture(t *testing.T, directory, cwd, id string, extra []map[string]any) string {
	t.Helper()
	rows := []map[string]any{
		{"type": "session_meta", "timestamp": "2026-11-10T01:00:00Z", "payload": map[string]any{"id": id, "cwd": cwd, "source": "exec", "cli_version": "0.167.0-alpha.3.2"}},
		{"type": "event_msg", "timestamp": "2026-11-10T01:00:01Z", "payload": map[string]any{"type": "task_started"}},
		{"type": "response_item", "timestamp": "2026-11-10T01:00:02Z", "payload": map[string]any{"type": "message", "role": "developer", "content": []map[string]string{{"type": "input_text", "text": "private developer instructions"}}}},
		{"type": "response_item", "timestamp": "2026-11-10T01:00:03Z", "payload": map[string]any{"type": "message", "role": "user", "content": []map[string]string{{"type": "input_text", "text": "项目正式发布前须检查工单状态。"}}}},
		{"type": "response_item", "timestamp": "2026-11-10T01:00:04Z", "payload": map[string]any{"type": "reasoning", "text": "private reasoning"}},
		{"type": "response_item", "timestamp": "2026-11-10T01:00:05Z", "payload": map[string]any{"type": "custom_tool_call", "name": "read", "call_id": "tool-1", "input": "synthetic tool request"}},
		{"type": "response_item", "timestamp": "2026-11-10T01:00:06Z", "payload": map[string]any{"type": "custom_tool_call_output", "call_id": "tool-1", "output": "synthetic tool result"}},
		{"type": "response_item", "timestamp": "2026-11-10T01:00:07Z", "payload": map[string]any{"type": "message", "role": "assistant", "phase": "final_answer", "content": []map[string]string{{"type": "output_text", "text": "这是用户确认的本项目发布约定。"}}}},
		{"type": "event_msg", "timestamp": "2026-11-10T01:00:08Z", "payload": map[string]any{"type": "task_complete"}},
	}
	rows = append(rows, extra...)
	var body strings.Builder
	for _, row := range rows {
		b, err := json.Marshal(row)
		if err != nil {
			t.Fatal(err)
		}
		body.Write(b)
		body.WriteByte('\n')
	}
	path := filepath.Join(directory, "sessions/2026/11/10", "rollout-synthetic-"+id+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body.String()), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func historyRepositoryFixture(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "repo")
	if b, err := exec.Command("/usr/bin/git", "init", root).CombinedOutput(); err != nil {
		t.Fatal(err, string(b))
	}
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

func historyHomeFixture(t *testing.T) string {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return home
}

func TestCodexHistoryAttributionRepeatedReadsVersionsAndExcludedStreams(t *testing.T) {
	ctx := context.Background()
	root := historyRepositoryFixture(t)
	home := historyHomeFixture(t)
	id := uuid.NewString()
	path := historyLogFixture(t, home, root, id, nil)
	outside := historyRepositoryFixture(t)
	foreign := historyLogFixture(t, home, outside, uuid.NewString(), nil)
	// Only the first metadata row may be read for a foreign repository.
	f, err := os.OpenFile(foreign, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("invalid foreign payload must not be parsed\n")
	f.Close()
	r, err := OpenCodexProjectHistory(ctx, home, uuid.NewString(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	page, err := r.List(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].State != "available" {
		t.Fatal(page)
	}
	selected := page.Items[0].ProjectSessionSelection
	for i := 0; i < 2; i++ {
		source, e := r.Read(ctx, selected)
		if e != nil {
			t.Fatal(e)
		}
		if !strings.Contains(source.Text, "synthetic tool result") || !strings.Contains(source.Text, "2026-11-10T01:00:03Z") || strings.Contains(source.Text, "private developer instructions") || strings.Contains(source.Text, "private reasoning") {
			t.Fatal(source.Text)
		}
	}
	f, err = os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("\n")
	f.Close()
	if _, err = r.Read(ctx, selected); err == nil {
		t.Fatal("changed selected raw log accepted")
	}
	if _, err = r.Read(ctx, records.ProjectSessionSelection{ID: "../../outside", Digest: selected.Digest}); err == nil {
		t.Fatal("arbitrary path accepted")
	}
	if err = os.Rename(root, root+"-old"); err != nil {
		t.Fatal(err)
	}
	if b, err := exec.Command("git", "init", root).CombinedOutput(); err != nil {
		t.Fatal(err, string(b))
	}
	if _, err = r.List(ctx, ""); err == nil {
		t.Fatal("replaced repository accepted")
	}
}

func TestCodexHistoryIncompleteMixedScopeDuplicateAndSymlink(t *testing.T) {
	for _, mode := range []string{"active", "mixed-scope", "duplicate", "symlink"} {
		t.Run(mode, func(t *testing.T) {
			root := historyRepositoryFixture(t)
			home := historyHomeFixture(t)
			id := uuid.NewString()
			var extra []map[string]any
			if mode == "active" {
				extra = []map[string]any{{"type": "event_msg", "timestamp": "2026-11-10T01:00:09Z", "payload": map[string]string{"type": "task_started"}}}
			}
			if mode == "mixed-scope" {
				extra = []map[string]any{{"type": "turn_context", "timestamp": "2026-11-10T01:00:09Z", "payload": map[string]string{"cwd": historyRepositoryFixture(t)}}}
			}
			path := historyLogFixture(t, home, root, id, extra)
			if mode == "duplicate" {
				body, _ := os.ReadFile(path)
				if err := os.WriteFile(filepath.Join(filepath.Dir(path), "rollout-duplicate-"+id+".jsonl"), body, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "symlink" {
				if err := os.Rename(path, path+"-original"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(path+"-original", path); err != nil {
					t.Fatal(err)
				}
			}
			r, err := OpenCodexProjectHistory(context.Background(), home, uuid.NewString(), root)
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			page, err := r.List(context.Background(), "")
			if mode == "duplicate" || mode == "symlink" {
				if err == nil {
					t.Fatal("ambiguous/linked source accepted", page)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(page.Items) != 1 || page.Items[0].State == "available" || page.Items[0].Digest != "" {
				t.Fatal("incomplete/mixed source offered", page)
			}
		})
	}
}
