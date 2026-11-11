package official

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

// Read exactly one completed session under the App-owned Codex home. No
// daily history, arbitrary path, developer instructions or internal reasoning
// enters the original user/assistant and tool stream.
func (c *Controller) Transcript(project, session, workspace string) (string, records.CodexOriginal, error) {
	var original records.CodexOriginal
	id, err := uuid.Parse(strings.TrimPrefix(session, "codex_"))
	if err != nil || session != "codex_"+id.String() {
		return "", original, errors.New("canonical Codex session required")
	}
	scope, err := c.LoadAgent(project)
	if err != nil {
		return "", original, err
	}
	if scope.Workspace != workspace {
		return "", original, errors.New("original Codex workspace is outside the approved project")
	}
	paths, err := filepath.Glob(filepath.Join(scope.Home, ".codex/sessions", "*", "*", "*", "rollout-*-"+id.String()+".jsonl"))
	if err != nil || len(paths) != 1 {
		return "", original, errors.New("the exact App-owned Codex session log is unavailable")
	}
	info, err := os.Lstat(paths[0])
	if err != nil || !info.Mode().IsRegular() {
		return "", original, errors.New("regular original session file required")
	}
	fd, err := syscall.Open(paths[0], syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return "", original, err
	}
	file := os.NewFile(uintptr(fd), paths[0])
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return "", original, errors.New("original session file changed while opening")
	}
	raw, err := io.ReadAll(io.LimitReader(file, 8*1024*1024+1))
	if err != nil {
		return "", original, err
	}
	if len(raw) > 8*1024*1024 {
		return "", original, errors.New("original session log exceeds 8 MiB")
	}
	var body strings.Builder
	finished, finalAnswer, metaValid := false, false, false
	for _, line := range bytes.Split(raw, []byte{'\n'}) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var row struct {
			Type, Timestamp string
			Payload         json.RawMessage
		}
		if err = json.Unmarshal(line, &row); err != nil {
			return "", original, errors.New("original session log is incomplete")
		}
		if row.Type == "session_meta" {
			var meta struct {
				ID, Cwd, Source string
				CLIVersion      string `json:"cli_version"`
			}
			if json.Unmarshal(row.Payload, &meta) != nil || meta.ID != id.String() || meta.Cwd != workspace || meta.Source != "exec" {
				return "", original, errors.New("original session identity differs")
			}
			original.Session, original.Workspace, original.CLIVersion, original.CapturedAt = session, workspace, meta.CLIVersion, row.Timestamp
			body.WriteString("Codex 原始会话 · " + session + "\n项目工作区：" + workspace + "\nCLI：" + meta.CLIVersion + "\n\n")
			metaValid = true
			continue
		}
		var p struct {
			Type, Role, Name, Phase, Input, Arguments string
			CallID                                    string `json:"call_id"`
			Output                                    json.RawMessage
			Content                                   []struct{ Type, Text string }
		}
		if json.Unmarshal(row.Payload, &p) != nil {
			return "", original, errors.New("invalid original session entry")
		}
		if row.Type == "event_msg" && p.Type == "task_complete" {
			finished = true
			continue
		}
		if row.Type != "response_item" {
			continue
		}
		switch p.Type {
		case "message":
			if p.Role != "user" && p.Role != "assistant" {
				continue
			}
			label := "用户"
			if p.Role == "assistant" {
				label = "助手"
			}
			for _, content := range p.Content {
				if content.Type != "input_text" && content.Type != "output_text" {
					continue
				}
				if p.Role == "user" && strings.HasPrefix(strings.TrimSpace(content.Text), "<environment_context>") {
					continue
				}
				body.WriteString(row.Timestamp + " · " + label + "\n" + content.Text + "\n\n")
			}
			if p.Role == "assistant" && p.Phase == "final_answer" {
				finalAnswer = true
			}
		case "function_call", "custom_tool_call":
			input := p.Arguments
			if p.Type == "custom_tool_call" {
				input = p.Input
			}
			body.WriteString(row.Timestamp + " · 工具请求 · " + p.Name + " · " + p.CallID + "\n" + input + "\n\n")
		case "function_call_output", "custom_tool_call_output":
			body.WriteString(row.Timestamp + " · 工具返回 · " + p.CallID + "\n")
			var text string
			if json.Unmarshal(p.Output, &text) == nil {
				body.WriteString(text)
			} else {
				var blocks []struct{ Type, Text string }
				if json.Unmarshal(p.Output, &blocks) != nil {
					return "", original, errors.New("unsupported original tool output format")
				}
				for _, block := range blocks {
					body.WriteString(block.Text + "\n")
				}
			}
			body.WriteString("\n\n")
		}
		if body.Len() > records.CogneeRawLimit {
			return "", original, errors.New("complete original dialogue exceeds 256 KiB; nothing truncated")
		}
	}
	if !metaValid || !finished || !finalAnswer {
		return "", original, errors.New("original Codex task has not completed")
	}
	hash := sha256.Sum256([]byte(body.String()))
	original.Digest = hex.EncodeToString(hash[:])
	return body.String(), original, nil
}
