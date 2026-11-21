package simpledesktop

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestPendingAutoCheckPresentsOnlyRealChecksAndRetriesReadOperation(t *testing.T) {
	a, q := projectQueueFixture()
	q.AutomaticSave, q.Loaded = true, true
	q.Next, q.BacklogNext = time.Now().Add(time.Hour), time.Now().Add(time.Hour)
	session := q.Sessions[1]
	key := session.Agent.ID + "/" + session.Binding.HostSessionKey
	q.Checks[key] = pendingCheckState{Status: "checking"}
	tt := ui.NewTester(a.View, 1200, 820)
	if !tt.HasText("正在检查") || tt.HasText("重新检查采集结果") || tt.HasText("核对采集结果") || !tt.HasText("自动采集已开启") {
		t.Fatal("checking state implies user action or an active send")
	}
	q.Checks[key] = pendingCheckState{Status: "unconfirmed", Reason: "original_receipt_unavailable", CheckedAt: 1700000000}
	tt.Frame()
	if tt.HasText("归档此异常") || tt.HasText("重新检查采集结果") || !tt.HasText("系统异常") {
		t.Fatal("missing original receipt has no archive action")
	}
	session.View.CheckDetailsOpen = true
	tt.Frame()
	if !tt.HasText("检查于 "+time.Unix(1700000000, 0).Format("01月02日 15:04")) || !tt.HasText("这批消息缺少可查询的唯一采集凭证，系统无法核实这次采集。") {
		t.Fatal("check details did not disclose its evidence boundary")
	}
	fixtureImage(t, tt, "pending-check-unconfirmed")
	q.Checks[key] = pendingCheckState{Status: "unconfirmed", Reason: "official_review_unavailable"}
	tt.Frame()
	token := filepath.Join(t.TempDir(), "manager.token")
	os.WriteFile(token, []byte("synthetic-management-token-123456789"), 0600)
	posts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts++
		if r.Method != "POST" || r.URL.Path != "/v1/projects/p/backlog/check" {
			t.Errorf("retry issued another operation: %s %s", r.Method, r.URL.Path)
		}
		var payload map[string]string
		json.NewDecoder(r.Body).Decode(&payload)
		if len(payload) != 2 || payload["agent_id"] != session.Agent.ID || payload["host"] != session.Binding.HostSessionKey {
			t.Error("check request included send parameters or lost original identity")
		}
		json.NewEncoder(w).Encode(map[string]any{"checks": []pendingCheckState{{AgentID: session.Agent.ID, Host: session.Binding.HostSessionKey, Status: "checking"}}})
	}))
	defer server.Close()
	a.Config.ProjectControl = &ProjectControlConfig{URL: server.URL, ManagementTokenFile: token}
	updates := make(chan func(), 10)
	a.Update = func(fn func()) { updates <- fn }
	tt.Scroll(1000, 700, 0, 600)
	clickProjectUI(t, tt, "重新检查采集结果")
	a.retryPendingCheck(q, a.access.Directory.Projects[0], session)
	drainReading(t, updates, func() bool { return q.BacklogNext.IsZero() })
	a.Update = nil
	tt.Frame()
	if posts != 1 || !tt.HasText("正在检查") || tt.HasText("重新检查采集结果") {
		t.Fatal("retry duplicated a check or failed to show actual running state")
	}
	q.BacklogError = "offline"
	tt.Frame()
	if !tt.HasText("重新检查采集结果") || tt.HasText("正在检查") {
		t.Fatal("lost service left a false perpetual spinner")
	}
}
