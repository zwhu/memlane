package simpledesktop

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/egoist/mygo/ui"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPendingProjectDocumentsUsesOfficialCompletionNotActivityGuess(t *testing.T) {
	completed := false
	include := true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Fatal("queue enumeration wrote data")
		}
		if r.URL.Path == "/api/v1/datasets/ds/processing-status" {
			if r.URL.Query().Get("pipeline") != "cognify_pipeline" {
				t.Error("wrong processing stage")
			}
			items := []map[string]any{{"id": "raw", "name": "design.md", "completed": completed}, {"id": "done", "name": "done.md", "completed": true}, {"id": "knowledge", "name": "Session learning", "completed": false}, {"id": "session-copy", "completed": false}, {"id": "trace-copy", "completed": false}}
			json.NewEncoder(w).Encode(map[string]any{"items": items})
			return
		}
		docs := []Document{{ID: "done", Dataset: "ds", Name: "done.md"}, {ID: "knowledge", Dataset: "ds", Name: "Session learning", Metadata: map[string]any{"node_set": []any{"session_learnings", "session_learnings:s"}}}, {ID: "session-copy", Dataset: "ds", Metadata: map[string]any{"node_set": []any{"user_sessions_from_cache"}}}, {ID: "trace-copy", Dataset: "ds", Metadata: map[string]any{"node_set": []any{"agent_trace_feedbacks"}}}}
		if include {
			docs = append(docs, Document{ID: "raw", Dataset: "ds", Name: "design.md"})
		}
		json.NewEncoder(w).Encode(docs)
	}))
	defer server.Close()
	c := Client{Config: Config{ServiceURL: server.URL}}
	rows, err := c.pendingProjectDocuments(context.Background(), Project{Dataset: "ds"})
	if err != nil || len(rows) != 1 || rows[0].ID != "raw" {
		t.Fatalf("completion classification failed: %#v %v", rows, err)
	}
	completed = true
	rows, err = c.pendingProjectDocuments(context.Background(), Project{Dataset: "ds"})
	if err != nil || len(rows) != 0 {
		t.Fatal("completed document kept pending")
	}
	completed = false
	include = false
	if _, err = c.pendingProjectDocuments(context.Background(), Project{Dataset: "ds"}); err == nil {
		t.Fatal("missing metadata faked successful empty list")
	}
}
func projectQueueFixture() (*App, *projectQueueState) {
	a := settingsFixture()
	a.page = "设置"
	a.access.OpenProject = "p"
	a.settingsOrigin = &readingLocation{Page: "知识", ProjectID: "p"}
	p := a.access.Directory.Projects[0]
	q := a.projectQueue(p)
	agent := a.access.Directory.Agents[0]
	q.Sessions = []queuedSession{
		{Agent: agent, Binding: ProjectBinding{ProjectID: "p", AgentID: agent.ID, HostSessionKey: "synthetic-first"}, View: &pendingBufferView{Loaded: true, Status: PendingMaterials{Pending: 2}}},
		{Agent: agent, Binding: ProjectBinding{ProjectID: "p", AgentID: agent.ID, HostSessionKey: "synthetic-second"}, View: &pendingBufferView{Loaded: true, Status: PendingMaterials{Pending: 1, Unknown: 1}}},
	}
	q.Checks = map[string]pendingCheckState{agent.ID + "/synthetic-second": {Status: "unconfirmed", Reason: "original_receipt_unavailable"}}
	for n, name := range []string{"项目界面设计与交互规范.md", "本地服务启动与故障排查.md"} {
		d := Document{ID: fmt.Sprint(n), Dataset: "ds", Name: name, Created: "2026-11-13T16:56:00Z"}
		q.Documents = append(q.Documents, Item{ID: d.ID, Dataset: d.Dataset, Kind: "文档", Title: name, Date: d.Created, Document: &d})
	}
	return a, q
}

func TestQueuedDocumentReaderDoesNotEnumerateAnotherCatalogAndRestoresScroll(t *testing.T) {
	raw := "Session ID: synthetic-session\nQuestion: 如何启动本地服务？\nAnswer: 使用项目开发启动器。"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || !strings.HasSuffix(r.URL.Path, "/raw") {
			t.Errorf("queue reader requested an unrelated catalog or mutation: %s %s", r.Method, r.URL.Path)
		}
		fmt.Fprint(w, raw)
	}))
	defer server.Close()
	a, _ := projectQueueFixture()
	a.Config.ServiceURL = server.URL
	a.Client = Client{Config: a.Config}
	a.readingIdentity, _ = readingIdentity(a.Client)
	updates := make(chan func(), 10)
	a.Update = nil
	p := a.access.Directory.Projects[0]
	q := a.projectQueueStateFor(p)
	q.Loaded, q.Next, q.BacklogNext = true, time.Now().Add(time.Hour), time.Now().Add(time.Hour)
	d := Document{ID: "source", Dataset: "ds", Name: "text_0123456789abcdef0123456789abcdef"}
	item := Item{ID: d.ID, Dataset: d.Dataset, Kind: "文档", Title: d.Name, Document: &d}
	q.Documents = []Item{item}
	for n := 0; n < 12; n++ {
		q.Sessions = append(q.Sessions, queuedSession{Agent: AccessAgent{Name: fmt.Sprint("Agent ", n)}, View: &pendingBufferView{}})
	}
	tt := ui.NewTester(func(c *ui.Context) { ui.Column(c).Fill().Children(func() { a.projectSettingsPage(c) }) }, 1200, 820)
	tt.Scroll(1000, 700, 0, 10000)
	before := q.Scroll.Y
	if before == 0 {
		t.Fatal("fixture did not reach a scrolled queue")
	}
	a.Update = func(fn func()) { updates <- fn }
	clickProjectUI(t, tt, "查看")
	reader := q.Reader
	drainReading(t, updates, func() bool { return !reader.Reading })
	a.Update = nil
	tt.Frame()
	if a.page != "设置" || reader.Error != "" || reader.Selected == nil || !reader.Selected.BodyLoaded || !tt.HasText("如何启动本地服务？") || !tt.HasText("刷新处理状态") {
		t.Fatal("pending detail lost its body, title, context or next read action")
	}
	if tt.HasText("已载入 0 条") || tt.HasText("选中内容已不在当前官方页面，请重新选择") || tt.HasText("Session ID: synthetic-session") {
		t.Fatal("queue reader leaked catalog errors or technical wrapper")
	}
	fixtureImage(t, tt, "queue-document-detail")
	clickProjectUI(t, tt, "返回待处理内容")
	if q.Reader != nil || q.Scroll.Y != before || a.access.OpenProject != p.ID {
		t.Fatal("return lost project or scroll position", q.Scroll.Y, before)
	}
}

func TestProjectQueueActionsShareOneColumn(t *testing.T) {
	for _, size := range [][2]int{{1200, 820}, {1440, 900}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			a, q := projectQueueFixture()
			q.Sessions = append(q.Sessions, queuedSession{Agent: AccessAgent{Name: "本机 pi"}, View: &pendingBufferView{}})
			// Render the queue in its production width; preceding settings cards
			// may place these rows below the viewport.
			tt := ui.NewTester(func(c *ui.Context) {
				ui.Column(c).FillWidth().MaxWidth(900).Children(func() {
					a.projectQueueSection(c, a.access.Directory.Projects[0])
				})
			}, size[0], size[1])
			check, ok := tt.Find("归档此异常")
			if !ok {
				t.Fatal("missing check action")
			}
			for _, label := range []string{"重新读取", "查看"} {
				r, ok := tt.Find(label)
				if !ok || r.X+r.W != check.X+check.W {
					t.Fatalf("%s is not aligned with check: %v vs %v", label, r, check)
				}
			}
			unsaved, ok := tt.Find("未保存")
			uncertain, found := tt.Find("暂无法确认")
			if !ok || !found || unsaved.X+unsaved.W != uncertain.X+uncertain.W {
				t.Fatalf("queue statuses shift when an action is absent: %v vs %v", unsaved, uncertain)
			}
			if delta := (uncertain.Y + uncertain.H/2) - (check.Y + check.H/2); delta < -1 || delta > 1 {
				t.Fatalf("status and text action are not vertically centered: %v vs %v", uncertain, check)
			}
		})
	}
}
func TestProjectQueueBothTypesAndCompactPermissionFeedback(t *testing.T) {
	for _, wh := range [][2]int{{1200, 820}, {1440, 900}} {
		t.Run(fmt.Sprint(wh), func(t *testing.T) {
			a, q := projectQueueFixture()
			tt := ui.NewTester(a.View, wh[0], wh[1])
			for _, label := range []string{"权限设置", "待处理内容", "会话保存", "待整理", "2 条待保存 · 1 条保存状态待确认", "2 份文档", "暂无法确认"} {
				if !tt.HasText(label) {
					t.Fatal("missing queue distinction", label)
				}
			}
			fixtureImage(t, tt, fmt.Sprintf("project-settings-%dx%d-both", wh[0], wh[1]))
			a.access.CheckboxPending = map[string]*projectCheckboxPending{"p/agent": {Project: "p", Agent: "agent", Field: "access", Value: "read", Revision: 4}}
			a.access.Busy = true
			tt.Frame()
			if !tt.HasText("保存中…") || tt.HasText("重新核对") || tt.HasText("修改结果待确认") {
				t.Fatal("normal saving looks like unknown failure")
			}
			fixtureImage(t, tt, fmt.Sprintf("project-settings-%dx%d-saving", wh[0], wh[1]))
			a.access.Busy = false
			tt.Frame()
			if !tt.HasText("尚未确认保存") || !tt.HasText("重新核对") {
				t.Fatal("unknown save has no compact recovery")
			}
			link, ok := tt.Find("重新核对")
			if !ok || link.W > 120 {
				t.Fatal("recovery expanded into a full-width button")
			}
			a.access.CheckboxPending = nil
			q.Sessions = nil
			q.Documents = nil
			tt.Frame()
			if !tt.HasText("当前没有未保存的记录") || !tt.HasText("当前没有待整理的文档") {
				t.Fatal("successful empty states missing")
			}
			fixtureImage(t, tt, fmt.Sprintf("project-settings-%dx%d-empty", wh[0], wh[1]))
			q.Loaded = false
			q.Loading = true
			q.Next = time.Now().Add(time.Hour)
			tt.Frame()
			if tt.HasText("0 条记录") || tt.HasText("0 份文档") {
				t.Fatal("loading faked zero counts")
			}
		})
	}
}
