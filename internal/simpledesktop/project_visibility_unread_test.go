package simpledesktop

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/app/api"
)

func notificationMenuFixture(t *testing.T) (*App, *projectQueueState, AccessProject) {
	t.Helper()
	a, q := projectQueueFixture()
	a.Config.ClientStateFile = filepath.Join(t.TempDir(), "client-state.json")
	a.Config.ProjectControl = &ProjectControlConfig{URL: "http://127.0.0.1:16135", ManagementTokenFile: p1TempToken(t)}
	a.Client.HTTP = &http.Client{Transport: queueTestTransport(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/reading-state/mark-all-read" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(api.MarkReadResponse{ReadingStateView: api.ReadingStateView{StateRev: 2}})
	})}
	t.Cleanup(a.waitKnowledgeReadFlush)
	p := a.access.Directory.Projects[0]
	a.Update = nil
	q.Next, q.BacklogNext = time.Now().Add(time.Hour), time.Now().Add(time.Hour)
	applyServerUnreadSnapshot(q, 1, 1, false)
	q.UnreadPreview = []Item{{ID: "new", Dataset: p.Dataset}}
	return a, q, p
}

func TestProjectClearNotificationsServerIsolationAndNewContent(t *testing.T) {
	a, q, p := notificationMenuFixture(t)
	other := AccessProject{ID: "other-project", Dataset: "other-dataset"}
	otherQ := a.projectQueueStateFor(other)
	applyServerUnreadSnapshot(otherQ, 1, 1, false)
	beforeFailed := q.InboxFailed
	if err := a.clearProjectNotifications(q, p); err != nil {
		t.Fatal(err)
	}
	if q.unreadCount() != 0 || len(q.UnreadPreview) != 0 || q.ServerUnreadStateRev != 2 || q.InboxFailed != beforeFailed || otherQ.unreadCount() != 1 {
		t.Fatal("mark-all changed another scope or retained unread knowledge")
	}
	applyServerUnreadSnapshot(q, 1, 2, false)
	if q.unreadCount() != 1 {
		t.Fatal("new content should be unread after mark-all")
	}
}

func TestProjectNotificationContextMenuCloseAndChoose(t *testing.T) {
	a, q, _ := notificationMenuFixture(t)
	tt := ui.NewTester(a.View, 1200, 820)
	if err := tt.RightClick("memlane"); err != nil {
		t.Fatal(err)
	}
	tt.CloseMenu()
	if q.unreadCount() != 1 || q.InboxFailed == 0 {
		t.Fatal("closing menu cleared notifications")
	}
	page, project, origin := a.page, a.project, a.settingsOrigin
	if err := tt.RightClick("memlane"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("清空未读"); err != nil {
		t.Fatal(err)
	}
	if q.unreadCount() != 0 || a.page != page || a.project != project || a.settingsOrigin != origin {
		t.Fatal("context menu failed to clear only the project's unread notifications")
	}
	if !tt.HasText("已清空「memlane」的未读记忆") {
		t.Fatal("success feedback missing")
	}
}

func TestProjectClearNotificationsReportsWriteFailure(t *testing.T) {
	a, q, p := notificationMenuFixture(t)
	a.Client.HTTP = &http.Client{Transport: queueTestTransport(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	})}
	if err := a.clearProjectNotifications(q, p); err == nil || q.unreadCount() != 1 || q.InboxFailed == 0 {
		t.Fatal("write failure reported success or cleared state")
	}
}
