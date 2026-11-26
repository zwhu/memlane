package app

import (
	"testing"

	"github.com/zwhu/memlane/app/api"
)

func TestRawSessionFromAPIPreservesCountsAndSuppressesRawIDLabel(t *testing.T) {
	hydrated := rawSessionFromAPI(api.RawItem{
		SessionID: "antigravity-6431",
		DatasetID: "ds",
		Title:     "之前有个设计是 mcp 和 hooks 的取舍",
		Label:     "之前有个设计是 mcp 和 hooks 的取舍",
		MsgCount:  2,
		ToolCalls: 216,
	})
	if hydrated.Label != "之前有个设计是 mcp 和 hooks 的取舍" || hydrated.MsgCount != 2 || hydrated.ToolCalls != 216 {
		t.Fatalf("unexpected hydrated entry: %+v", hydrated)
	}

	rawOnly := rawSessionFromAPI(api.RawItem{
		SessionID: "antigravity-c9f08ea7",
		DatasetID: "ds",
		Title:     "antigravity-c9f08ea7",
		Label:     "",
		MsgCount:  0,
		ToolCalls: 1,
	})
	if rawOnly.Label != "" || rawOnly.ToolCalls != 1 {
		t.Fatalf("expected empty label when title equals raw session_id, got %+v", rawOnly)
	}
}
