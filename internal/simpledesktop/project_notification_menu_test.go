package simpledesktop

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func notificationMenuFixture(t *testing.T) (*App, *projectQueueState, AccessProject, []Document) {
	t.Helper()
	a, q, p := acknowledgementFixture(t)
	a.Update = nil
	q.Loaded = true
	q.Next, q.BacklogNext = time.Now().Add(time.Hour), time.Now().Add(time.Hour)
	q.Sessions[0].View.Status.UnknownEntryIDs = []string{noticeHash("original-message")}
	docs := []Document{unreadDoc("old"), unreadDoc("new")}
	a.observeKnowledgeCatalog(p, q, docs[:1])
	a.observeKnowledgeCatalog(p, q, docs)
	return a, q, p, docs
}

func TestProjectClearNotificationsPersistenceIsolationAndNewContent(t *testing.T) {
	a, q, p, docs := notificationMenuFixture(t)
	other := p
	other.ID, other.Dataset = "other-project", "other-dataset"
	otherQ := a.projectQueueStateFor(other)
	d := unreadDoc("other-knowledge")
	d.Dataset = other.Dataset
	a.observeKnowledgeCatalog(other, otherQ, nil)
	a.observeKnowledgeCatalog(other, otherQ, []Document{d})
	otherPath, _ := a.knowledgeReadPath(other)
	beforeOther, _ := os.ReadFile(otherPath)
	beforeMessage := q.Sessions[0].View.Status
	beforeJob, enabled := *q.Backlog, q.KnowledgeRead.StartedAt
	if q.unreadCount() != 1 || !q.unacknowledgedNotification(time.Now()) {
		t.Fatal("fixture must contain unread knowledge and an issue")
	}
	if err := a.clearProjectNotifications(q, p); err != nil {
		t.Fatal(err)
	}
	if q.unreadCount() != 0 || q.unacknowledgedNotification(time.Now()) || len(q.UnreadKnowledge) != 0 {
		t.Fatal("project notifications remained")
	}
	if !enabled.Equal(q.KnowledgeRead.StartedAt) || !reflect.DeepEqual(beforeMessage, q.Sessions[0].View.Status) || beforeJob != *q.Backlog {
		t.Fatal("clearing changed baseline, messages or task")
	}
	afterOther, _ := os.ReadFile(otherPath)
	if string(beforeOther) != string(afterOther) || otherQ.unreadCount() != 1 {
		t.Fatal("another project was changed")
	}
	a.access.Queues = nil
	reopened := a.projectQueueStateFor(p)
	reopened.Sessions, reopened.Backlog = q.Sessions, q.Backlog
	a.observeKnowledgeCatalog(p, reopened, docs)
	if reopened.unreadCount() != 0 || reopened.unacknowledgedNotification(time.Now()) {
		t.Fatal("reopen lost cleared state")
	}
	a.observeKnowledgeCatalog(p, reopened, append(docs, unreadDoc("later")))
	reopened.Sessions[0].View.Status.UnknownEntryIDs = []string{noticeHash("new-message")}
	if reopened.unreadCount() != 1 || !reopened.unacknowledgedNotification(time.Now()) {
		t.Fatal("new knowledge or issue inherited cleared state")
	}
}

func TestProjectNotificationContextMenuCloseAndChoose(t *testing.T) {
	a, q, _, _ := notificationMenuFixture(t)
	tt := ui.NewTester(a.View, 1200, 820)
	if err := tt.RightClick("memlane"); err != nil {
		t.Fatal(err)
	}
	tt.CloseMenu()
	if q.unreadCount() != 1 || !q.unacknowledgedNotification(time.Now()) {
		t.Fatal("closing menu cleared notifications")
	}
	page, project, origin := a.page, a.project, a.settingsOrigin
	if err := tt.RightClick("memlane"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("清空所有通知"); err != nil {
		t.Fatal(err)
	}
	if q.unreadCount() != 0 || q.unacknowledgedNotification(time.Now()) {
		t.Fatal("context menu did not clear the project")
	}
	if a.page != page || a.project != project || a.settingsOrigin != origin {
		t.Fatal("right-click changed the current reading location")
	}
	if !tt.HasText("已清空「memlane」的所有通知") {
		t.Fatal("success feedback missing")
	}
}

func TestProjectClearNotificationsRejectsCorruptState(t *testing.T) {
	for _, state := range []string{"knowledge", "acknowledgements", "missing-knowledge"} {
		t.Run(state, func(t *testing.T) {
			a, q, p, _ := notificationMenuFixture(t)
			path, _ := a.knowledgeReadPath(p)
			if state == "acknowledgements" {
				path, _ = a.noticeAcknowledgementPath(p)
			}
			if state == "missing-knowledge" {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.WriteFile(path, []byte("corrupt"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := a.clearProjectNotifications(q, p); err == nil {
				t.Fatal("corrupt state reported success")
			}
			b, _ := os.ReadFile(path)
			if (state != "missing-knowledge" && string(b) != "corrupt") || (state == "missing-knowledge" && len(b) != 0) || q.unreadCount() != 1 || len(q.NoticeAcknowledgements) != 0 {
				t.Fatal("failure replaced state or cleared reminders")
			}
		})
	}
}

func TestProjectClearNotificationsReportsWriteFailure(t *testing.T) {
	a, q, p, _ := notificationMenuFixture(t)
	// A read-only state directory still permits validation of existing files,
	// but the atomic writer cannot create its temporary file.
	dir := filepath.Dir(a.Config.ClientStateFile)
	if os.Geteuid() == 0 {
		t.Skip("root can write through the permission fixture")
	}
	if err := os.Chmod(dir, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0700) })
	if err := a.clearProjectNotifications(q, p); err == nil || q.unreadCount() != 1 || !q.unacknowledgedNotification(time.Now()) {
		t.Fatal("write failure reported success or cleared state")
	}
}
