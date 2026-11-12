package desktop

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/zwhu/memlane/internal/records"
)

func TestImportedCapturePresentationRequiresExactStoredEnvelope(t *testing.T) {
	fragment := map[string]string{"agent": "codex", "role": "user", "session": "original-session", "turn": "original-turn", "workspace": "/synthetic/repo", "timestamp": "2026-11-12T00:00:00Z", "timestamp_kind": "hook_observed"}
	encode := func(fields map[string]string) json.RawMessage {
		f, _ := json.Marshal(fields)
		m, _ := json.Marshal(map[string]any{"memlane_import_v1": map[string]any{"origin": "raw", "source_detail": map[string]any{"fragment": string(f)}}})
		return m
	}
	const prefix = "Codex 原始对话 · user\n会话：original-session\n轮次：original-turn\n工作目录：/synthetic/repo\n采集时间：2026-11-12T00:00:00Z\n\n"
	const body = "User message\n\n会话：literal text inside the original message."
	content := records.OfficialDocumentText{Document: records.OfficialDocument{Metadata: encode(fragment)}, Text: prefix + body}
	p := presentImportedSource(content)
	if p.Body != body || p.Role != "用户" || p.TimeLabel != "采集时间" || p.Session != "original-session" || content.Text != prefix+body {
		t.Fatal("recognized capture altered body, time meaning or raw text")
	}
	for _, mode := range []string{"unknown_metadata", "different_body_prefix", "embedded_prefix", "mismatched_source", "unknown_role", "ambiguous_time_kind"} {
		t.Run(mode, func(t *testing.T) {
			c := content
			fields := map[string]string{}
			for k, v := range fragment {
				fields[k] = v
			}
			switch mode {
			case "unknown_metadata":
				c.Document.Metadata = json.RawMessage(`{}`)
			case "different_body_prefix":
				c.Text = strings.Replace(c.Text, "采集时间", "消息时间", 1)
			case "embedded_prefix":
				c.Text = "User quote:\n" + c.Text
			case "mismatched_source":
				fields["session"] = "other-session"
				c.Document.Metadata = encode(fields)
			case "unknown_role":
				fields["role"] = "system"
				c.Document.Metadata = encode(fields)
			case "ambiguous_time_kind":
				fields["timestamp_kind"] = "message"
				c.Document.Metadata = encode(fields)
			}
			p := presentImportedSource(c)
			if p.Body != c.Text || p.Role != "" || p.Session != "" {
				t.Fatal("unverified envelope removed or invented attribution")
			}
		})
	}
}
