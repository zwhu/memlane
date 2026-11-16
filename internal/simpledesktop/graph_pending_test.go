package simpledesktop

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestGraphPendingShowsOfficialUnprocessedScopeWithoutStartingWork(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/v1/projects/p/tool-graph/preview" {
			t.Error("unexpected mutation or project", r.Method, r.URL.Path)
		}
		fmt.Fprint(w, `{"session_count":2,"tool_steps":47,"review_token":"scope"}`)
	}))
	defer server.Close()
	token := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(token, []byte(strings.Repeat("synthetic", 5)), 0600); err != nil {
		t.Fatal(err)
	}
	a := projectUIFixture()
	a.Config.ProjectControl = &ProjectControlConfig{URL: server.URL, ManagementTokenFile: token}
	updates := make(chan func(), 4)
	a.Update = func(fn func()) { updates <- fn }
	p, _ := a.current()
	a.loadGraphPending(p)
	drainReading(t, updates, func() bool { return !a.graphPending.Busy })
	tt := ui.NewTester(func(c *ui.Context) { ui.Column(c).Fill().Children(func() { a.graphPendingHint(c, p) }) }, 1200, 820)
	if !tt.HasText("有 2 个会话、47 条工具记录尚未整理为图谱。") || !tt.HasText("查看整理范围") {
		t.Fatal("pending official scope is not visible")
	}
	fixtureImage(t, tt, "graph-pending-hint")
	a.graphPending.Preview.Steps = 0
	tt.Frame()
	if tt.HasText("查看整理范围") {
		t.Fatal("empty scope displayed as pending")
	}
	a.graphPending.Preview = nil
	a.graphPending.Error = "待整理工具记录暂无法核对"
	tt.Frame()
	if !tt.HasText("待整理工具记录暂无法核对") {
		t.Fatal("failure silently shown as no pending work")
	}
	other := p
	other.ID = "another"
	tt = ui.NewTester(func(c *ui.Context) { ui.Column(c).Fill().Children(func() { a.graphPendingHint(c, other) }) }, 1200, 820)
	if tt.HasText("待整理工具记录暂无法核对") {
		t.Fatal("scope leaked across projects")
	}
}
