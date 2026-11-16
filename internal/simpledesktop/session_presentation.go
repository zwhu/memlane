package simpledesktop

import (
	"fmt"
	"strings"
)

// Stable host identity is separate from the first captured question. A paused
// capture may first see a much later turn, and the official detail is a tail.
func sessionIdentity(id string) string {
	host, key, ok := strings.Cut(id, "_")
	if !ok {
		host, key = "Agent", id
	}
	switch strings.ToLower(host) {
	case "codex":
		host = "Codex"
		if strings.HasPrefix(key, "traex-") {
			host = "TraeX"
		} else if strings.HasPrefix(key, "pi-") {
			host = "pi"
		}
	case "claude":
		host = "Claude Code"
	case "pi":
		host = "pi"
	case "traex":
		host = "TraeX"
	}
	r := []rune(key)
	if len(r) > 18 {
		key = string(r[:8]) + "…" + string(r[len(r)-8:])
	}
	return host + " 会话 · " + key
}

func sessionListMeta(i Item) string {
	return "最近活动 · " + displayDate(i.Date)
}

func sessionCoverage(d SessionDetail) string {
	return fmt.Sprintf("已保存 %d 轮问答 · %d 条工具记录", d.Count, d.ToolCount)
}
