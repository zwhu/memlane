package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

func TestRawPagePreservesSubagentPreviewAndOrphanFlags(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/raw-records" || r.URL.Query().Get("project_id") != "p-sub" {
			http.Error(w, "unexpected request: "+r.URL.String(), http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.RawRecordsPage{
			Items: []api.RawItem{
				{
					DatasetID:       "ds-sub",
					ProjectID:       "p-sub",
					Kind:            "session",
					SessionID:       "parent-1",
					UserID:          "u1",
					Title:           "主会话任务",
					Label:           "主会话任务",
					MsgCount:        2,
					ToolCalls:       3,
					IsTopLevel:      true,
					ChildCount:      6,
					ChildrenHasMore: true,
					ChildrenPreview: []api.RawItem{
						{
							DatasetID:       "ds-sub",
							Kind:            "session",
							SessionID:       "child-1",
							UserID:          "u1",
							Title:           "子会话研究",
							Label:           "子会话研究",
							MsgCount:        1,
							ToolCalls:       4,
							IsSubagent:      true,
							ParentSessionID: "parent-1",
							SubagentRole:    "research",
							NestingDepth:    1,
						},
					},
				},
				{
					DatasetID:    "ds-sub",
					ProjectID:    "p-sub",
					Kind:         "session",
					SessionID:    "orphan-1",
					UserID:       "u1",
					Title:        "orphan-1",
					ToolCalls:    5,
					IsTopLevel:   true,
					IsSubagent:   true,
					SubagentRole: "explorer",
					NestingDepth: 1,
				},
			},
		})
	}))
	defer server.Close()

	tokenFile := filepath.Join(t.TempDir(), "manager.token")
	if err := os.WriteFile(tokenFile, []byte(strings.Repeat("t", 32)), 0o600); err != nil {
		t.Fatal(err)
	}
	client := ProjectControlClient{Config: ProjectControlConfig{URL: server.URL, ManagementTokenFile: tokenFile}}
	resp, err := client.rawPage(context.Background(), "p-sub", "session", "")
	if err != nil {
		t.Fatal(err)
	}
	items, err := v2RawRecordsToItems("ds-sub", resp)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d: %+v", len(items), items)
	}
	parent := items[0]
	if parent.SubagentTotalCount != 6 || !parent.SubagentsHasMore || len(parent.Subagents) != 1 {
		t.Fatalf("parent subagents mapping failed: total=%d hasMore=%v subs=%+v",
			parent.SubagentTotalCount, parent.SubagentsHasMore, parent.Subagents)
	}
	if child := parent.Subagents[0]; child.ID != "child-1" || child.Session == nil || !child.Session.IsSubagent || child.Session.SubagentRole != "research" || child.ParentTitle != "主会话任务" {
		t.Fatalf("mapped preview child = %+v", child)
	}
	orphan := items[1]
	if orphan.Session == nil || !orphan.Session.IsSubagent || orphan.Session.SubagentRole != "explorer" {
		t.Fatalf("orphan subagent flag lost through /api/raw-records: %+v", orphan.Session)
	}
}
