package simpledesktop

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestSubagentUILinkingQAsToolsAndReturnLightDark(t *testing.T) {
	now := time.Now().UTC()
	parentID := "codex_parent-main"
	childQAID := "antigravity-child-qa"
	childToolsID := "antigravity-child-tools"
	childNestedID := "codex_child-nested"
	orphanID := "antigravity-orphan-1"

	rawResp := struct{ Sessions []rawSessionEntry }{
		Sessions: []rawSessionEntry{
			{
				Session:   Session{ID: parentID, Dataset: "ds-sub", User: "u1", Updated: now.Add(-1 * time.Minute).Format(time.RFC3339)},
				Label:     "多 Agent 主会话规划与验证",
				MsgCount:  1,
				ToolCalls: 2,
			},
			{
				Session: Session{
					ID:              childQAID,
					Dataset:         "ds-sub",
					User:            "u1",
					Updated:         now.Add(-2 * time.Minute).Format(time.RFC3339),
					IsSubagent:      true,
					ParentSessionID: parentID,
					SubagentRole:    "research",
					NestingDepth:    1,
				},
				Label:     "排查子会话并发",
				MsgCount:  1,
				ToolCalls: 2,
			},
			{
				Session: Session{
					ID:              childToolsID,
					Dataset:         "ds-sub",
					User:            "u1",
					Updated:         now.Add(-3 * time.Minute).Format(time.RFC3339),
					IsSubagent:      true,
					ParentSessionID: parentID,
					SubagentRole:    "image-generator",
					NestingDepth:    1,
				},
				MsgCount:  0,
				ToolCalls: 1,
			},
			{
				Session: Session{
					ID:              childNestedID,
					Dataset:         "ds-sub",
					User:            "u1",
					Updated:         now.Add(-4 * time.Minute).Format(time.RFC3339),
					IsSubagent:      true,
					ParentSessionID: childQAID,
					SubagentRole:    "worker",
					NestingDepth:    2,
				},
				Label:     "执行嵌套核查",
				MsgCount:  1,
				ToolCalls: 1,
			},
			{
				Session: Session{
					ID:           orphanID,
					Dataset:      "ds-sub",
					User:         "u1",
					Updated:      now.Add(-5 * time.Minute).Format(time.RFC3339),
					IsSubagent:   true,
					SubagentRole: "explorer",
					NestingDepth: 1,
				},
				MsgCount:  0,
				ToolCalls: 1,
			},
		},
	}

	feed := v2RawRecordsResponse{DatasetID: "ds-sub", CacheReady: true}
	for _, index := range []int{0, 4} {
		row := rawResp.Sessions[index]
		entry := v2RawSessionEntry{Session: row.Session, Label: row.Label, MsgCount: row.MsgCount, ToolCalls: row.ToolCalls}
		if index == 0 {
			entry.ChildCount, entry.ChildrenPreview = 3, rawResp.Sessions[1:4]
		}
		feed.Items = append(feed.Items, v2RawRecordItem{ItemKind: "session", Session: &entry})
	}
	histories := map[string]SessionHistoryPage{
		parentID: {
			Paging: true,
			Limit:  20,
			Subagents: []rawSessionEntry{
				rawResp.Sessions[1],
				rawResp.Sessions[2],
				rawResp.Sessions[3],
			},
			SessionDetail: SessionDetail{
				Session:   rawResp.Sessions[0].Session,
				Label:     "多 Agent 主会话规划与验证",
				Count:     1,
				ToolCount: 1,
				QAs:       []QA{{ID: "p-q1", Question: "多 Agent 主会话规划与验证", Answer: "主会话协调回答"}},
				Traces:    []Trace{{ID: "p-t1", Name: "Read", Status: "success"}},
			},
		},
		childQAID: {
			Paging:      true,
			Limit:       20,
			ParentLabel: "多 Agent 主会话规划与验证",
			SessionDetail: SessionDetail{
				Session:   rawResp.Sessions[1].Session,
				Label:     "排查子会话并发",
				Count:     1,
				ToolCount: 1,
				QAs:       []QA{{ID: "cq-1", Question: "Research prompt from coordinator", Answer: "Agent-to-agent finding details"}},
				Traces:    []Trace{{ID: "ct-1", Name: "Grep", Status: "success"}},
			},
		},
		childToolsID: {
			Paging:      true,
			Limit:       20,
			ParentLabel: "多 Agent 主会话规划与验证",
			SessionDetail: SessionDetail{
				Session:   rawResp.Sessions[2].Session,
				Count:     0,
				ToolCount: 1,
				QAs:       []QA{},
				Traces:    []Trace{{ID: "ig-1", Name: "generate_image", Status: "success"}},
			},
		},
		orphanID: {
			Paging: true,
			Limit:  20,
			SessionDetail: SessionDetail{
				Session:   rawResp.Sessions[4].Session,
				Count:     0,
				ToolCount: 1,
				QAs:       []QA{},
				Traces:    []Trace{{ID: "or-1", Name: "Glob", Status: "success"}},
			},
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/capabilities":
			http.NotFound(w, r)
		case "/api/raw-records":
			http.NotFound(w, r)
		case "/v1/projects/p-sub/raw-records":
			_ = json.NewEncoder(w).Encode(feed)
		case "/v1/raw-records/session-history":
			sid := r.URL.Query().Get("session_id")
			page, ok := histories[sid]
			if !ok {
				http.Error(w, "not found", 404)
				return
			}
			_ = json.NewEncoder(w).Encode(page)
		default:
			http.Error(w, "unexpected path: "+r.URL.Path, 500)
		}
	}))
	defer server.Close()

	dir := t.TempDir()
	tokenFile := filepath.Join(dir, "manager.token")
	if err := os.WriteFile(tokenFile, []byte(strings.Repeat("m", 40)), 0600); err != nil {
		t.Fatal(err)
	}

	for _, appearance := range []string{"light", "dark"} {
		t.Run(appearance, func(t *testing.T) {
			cfg := Config{
				ServiceURL:     server.URL,
				ProjectControl: &ProjectControlConfig{URL: server.URL, ManagementTokenFile: tokenFile},
				Projects:       []Project{{ID: "p-sub", Name: "memlane", Path: "/synthetic/memlane", Dataset: "ds-sub"}},
			}
			a := New(cfg, nil)
			setTestAppearance(t, a, appearance)
			a.access.Loaded = true
			a.access.Next = time.Now().Add(time.Hour)
			a.service = "已连接"
			updates := make(chan func(), 64)
			a.Update = func(fn func()) { updates <- fn }
			t.Cleanup(func() { a.cancelAndResetPanes() })

			a.navigateKind("知识", 0, "原始记录")
			s := a.state()
			drainReading(t, updates, func() bool { return s.Loaded && !s.Loading && !s.Reading })
			tt := ui.NewTester(a.View, 1200, 820)
			tt.Frame()

			// 1. Left list collapses linked children under parent badge and keeps orphan flagged cleanly.
			if len(s.Items) != 2 {
				t.Fatalf("[%s] expected 2 items in list (parent + orphan), got %d", appearance, len(s.Items))
			}
			if !tt.HasText("含 3 子会话") || !tt.HasText("子会话 · explorer") {
				t.Fatalf("[%s] missing subagent badges in left list: %v", appearance, tt.Texts())
			}
			if !tt.HasText("子会话 (3)") {
				t.Fatalf("[%s] expected 3-way tab with 子会话 (3) on parent session: %v", appearance, tt.Texts())
			}

			// 2. Switch to 子会话 (3) tab -> see BOTH QA+tool subagent and 0-QA tool-only subagent.
			clickProjectUI(t, tt, "子会话 (3)")
			if !s.SessionSubagents || !tt.HasText("排查子会话并发") || !tt.HasText("已保存 1 轮问答 · 2 条工具记录") || !tt.HasText("已保存 0 轮问答 · 1 条工具记录") {
				t.Fatalf("[%s] subagents list view missing QA/tool subagent cards: %v", appearance, tt.Texts())
			}

			// 3. Click research subagent (has QA + tools) -> opens full reader defaulting to 问答 with 上游消息 label.
			clickProjectUI(t, tt, "子会话 · research：排查子会话并发")
			drainReading(t, updates, func() bool {
				return !s.Reading && s.Selected != nil && s.Selected.ID == childQAID && s.SourcePage != nil
			})
			tt.Frame()
			if s.SessionTools {
				t.Fatalf("[%s] QA-containing subagent should default to 问答 tab, not 工具记录", appearance)
			}
			if !tt.HasText("返回主会话") || !tt.HasText("上游消息") || !tt.HasText("Research prompt from coordinator") || !tt.HasText("Agent-to-agent finding details") {
				t.Fatalf("[%s] QA subagent reader missing expected dialogue or parent bar: %v", appearance, tt.Texts())
			}
			// Can also switch to 工具记录 on the same subagent!
			clickProjectUI(t, tt, "工具记录")
			if !s.SessionTools || !tt.HasText("Grep") {
				t.Fatalf("[%s] failed switching to 工具记录 on QA+tools subagent: %v", appearance, tt.Texts())
			}

			// 4. Click 返回主会话 -> instantaneously returns to parent's 子会话 (3) tab.
			clickProjectUI(t, tt, "返回主会话")
			tt.Frame()
			if s.Selected == nil || s.Selected.ID != parentID || !s.SessionSubagents {
				t.Fatalf("[%s] expected immediate return to parent subagents tab, got selected=%v subagentsTab=%v", appearance, s.Selected, s.SessionSubagents)
			}

			// 5. Click 0-QA tool-only subagent -> opens full reader defaulting to 工具记录 tab, with 问答 still reachable.
			clickProjectUI(t, tt, fmt.Sprintf("子会话 · image-generator：子会话 · image-generator"))
			drainReading(t, updates, func() bool {
				return !s.Reading && s.Selected != nil && s.Selected.ID == childToolsID && s.SourcePage != nil
			})
			tt.Frame()
			if !s.SessionTools || !tt.HasText("generate_image") {
				t.Fatalf("[%s] 0-QA subagent should default to 工具记录 and show traces: %v", appearance, tt.Texts())
			}
			clickProjectUI(t, tt, "问答")
			if s.SessionTools || !tt.HasText("此会话尚无已保存的问答") {
				t.Fatalf("[%s] 问答 tab should still be inspectable on tool-only subagent: %v", appearance, tt.Texts())
			}

			// 6. Direct jump to child subagent (no cached SubagentParent) -> 返回主会话 loads parent cleanly.
			a.selectItem(s, s.Items[0].Subagents[0])
			drainReading(t, updates, func() bool {
				return !s.Reading && s.Selected != nil && s.Selected.ID == childQAID && s.SourcePage != nil
			})
			tt.Frame()
			if s.SubagentParent != nil {
				t.Fatal("selectItem must clear SubagentParent")
			}
			clickProjectUI(t, tt, "返回主会话")
			drainReading(t, updates, func() bool {
				return !s.Reading && s.Selected != nil && s.Selected.ID == parentID && s.SourcePage != nil
			})
			tt.Frame()
			if !tt.HasText("多 Agent 主会话规划与验证") {
				t.Fatalf("[%s] direct-jump return to parent failed: %v", appearance, tt.Texts())
			}
		})
	}
}
