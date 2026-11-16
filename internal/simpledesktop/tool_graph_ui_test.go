package simpledesktop

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestToolGraphSettingsDoNotInvalidateReadingPermissions(t *testing.T) {
	a := AccessDirectory{Projects: []AccessProject{{ID: "p"}}}
	b := AccessDirectory{ToolGraph: ToolGraphSettings{GlobalEnabled: true}, Projects: []AccessProject{{ID: "p", ToolGraph: ToolGraphSettings{Effective: true}}}}
	if accessPolicyChanged(a, b) {
		t.Fatal("graph preference invalidated reading policy")
	}
}

func TestToolGraphGlobalProjectSettingsAndCostConfirmation(t *testing.T) {
	a := projectUIFixture()
	a.access.Loaded = true
	p := AccessProject{ID: "p", Name: "项目", ToolGraph: ToolGraphSettings{Source: "global"}}
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Padding(24).Gap(20).Children(func() { a.globalToolGraphSettings(c); a.projectToolGraphSettings(c, p) })
	}, 1200, 820)
	for _, text := range []string{"自动构建关系图谱", "跟随全局", "开启", "关闭", "创建工具图谱"} {
		if !tt.HasText(text) {
			t.Fatal("missing setting", text)
		}
	}
	projectOnly := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Padding(24).Children(func() { a.projectToolGraphSettings(c, p) })
	}, 1200, 820)
	title, titleOK := projectOnly.Find("工具记录关系图谱")
	action, actionOK := projectOnly.Find("创建工具图谱")
	if !titleOK || !actionOK || action.X <= title.X || action.Y > title.Y+title.H {
		t.Fatal("create action is not in the card header", title, action)
	}
	if tt.HasText("记忆提炼完成后，在后台构建当前项目的实体关系图谱。") || tt.HasText("更改仅影响后续任务，不会删除已有图谱或打断当前任务。") {
		t.Fatal("project card still repeats explanatory paragraphs")
	}
	fixtureImage(t, tt, "tool-graph-settings")
	a.toolGraph = toolGraphUIState{Open: true, Project: "p", Preview: &toolGraphPreview{Sessions: 2, Steps: 7, Token: "reviewed"}}
	tt = ui.NewTester(func(c *ui.Context) { ui.Column(c).Fill(); a.toolGraphDialog(c) }, 1200, 820)
	for _, text := range []string{"创建工具图谱", "开始创建", "使用当前模型，会消耗额度。"} {
		if !tt.HasText(text) {
			t.Fatal("missing reviewed/cost boundary", text)
		}
	}
	if tt.HasText("重新核对范围") || tt.HasText("刷新数量") || !tt.HasText("取消") {
		t.Fatal("creation confirmation contains an unnecessary refresh action")
	}
	fixtureImage(t, tt, "tool-graph-confirmation")
}

func TestToolGraphStartPreservesOriginalOperationAndDoesNotRepeat(t *testing.T) {
	var posts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/v1/projects/p/tool-graph" {
			t.Error("unexpected graph operation", r.Method, r.URL.Path)
		}
		posts.Add(1)
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["review_token"] != "reviewed" {
			t.Error("reviewed scope was dropped")
		}
		json.NewEncoder(w).Encode(map[string]any{"operation_id": body["operation_id"], "status": "queued"})
	}))
	defer server.Close()
	token := filepath.Join(t.TempDir(), "manager-token")
	if err := os.WriteFile(token, []byte(strings.Repeat("synthetic", 5)), 0600); err != nil {
		t.Fatal(err)
	}
	a := projectUIFixture()
	a.Config.ProjectControl = &ProjectControlConfig{URL: server.URL, ManagementTokenFile: token}
	updates := make(chan func(), 10)
	a.Update = func(fn func()) { updates <- fn }
	a.toolGraph = toolGraphUIState{Open: true, Project: "p", Preview: &toolGraphPreview{Sessions: 1, Steps: 3, Token: "reviewed"}}
	a.toolGraphAction("start")
	a.toolGraphAction("start") // In-flight re-entry cannot allocate another job.
	drainReading(t, updates, func() bool { return !a.toolGraph.Busy })
	if posts.Load() != 1 || a.toolGraph.Job == nil || a.toolGraph.Job.Operation == "" || a.toolGraph.Job.Status != "queued" {
		t.Fatal("job identity/re-entry", posts.Load(), a.toolGraph.Job)
	}
}
