package simpledesktop

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/egoist/mygo/ui"
	"net/http"
	"net/http/httptest"
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
			items := []map[string]any{{"id": "raw", "name": "design.md", "completed": completed}, {"id": "done", "name": "done.md", "completed": true}, {"id": "knowledge", "name": "Session learning", "completed": false}}
			json.NewEncoder(w).Encode(map[string]any{"items": items})
			return
		}
		docs := []Document{{ID: "done", Dataset: "ds", Name: "done.md"}, {ID: "knowledge", Dataset: "ds", Name: "Session learning", Metadata: map[string]any{"node_set": []any{"session_learnings", "session_learnings:s"}}}}
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
	for n, name := range []string{"项目界面设计与交互规范.md", "本地服务启动与故障排查.md"} {
		d := Document{ID: fmt.Sprint(n), Dataset: "ds", Name: name, Created: "2026-11-13T16:56:00Z"}
		q.Documents = append(q.Documents, Item{ID: d.ID, Dataset: d.Dataset, Kind: "文档", Title: name, Date: d.Created, Document: &d})
	}
	return a, q
}
func TestProjectQueueBothTypesAndCompactPermissionFeedback(t *testing.T) {
	for _, wh := range [][2]int{{1200, 820}, {1440, 900}} {
		t.Run(fmt.Sprint(wh), func(t *testing.T) {
			a, q := projectQueueFixture()
			tt := ui.NewTester(a.View, wh[0], wh[1])
			for _, label := range []string{"权限设置", "待处理文档", "待保存与核对", "待整理", "3 条记录", "2 份文档", "保存结果待核对"} {
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
