package simpledesktop

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func notificationMenuFixture(t *testing.T) (*App, *projectQueueState, AccessProject, []Document) {
	t.Helper()
	a, q := projectQueueFixture()
	a.Config.ClientStateFile = filepath.Join(t.TempDir(), "state.json")
	t.Cleanup(a.waitKnowledgeReadFlush)
	p := a.access.Directory.Projects[0]
	a.Update = nil
	q.Loaded = true
	q.Next, q.BacklogNext = time.Now().Add(time.Hour), time.Now().Add(time.Hour)
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
	beforeFailed, enabled := q.InboxFailed, q.KnowledgeRead.StartedAt
	if q.unreadCount() != 1 || q.InboxFailed == 0 {
		t.Fatal("fixture must contain unread knowledge and an issue")
	}
	if err := a.clearProjectNotifications(q, p); err != nil {
		t.Fatal(err)
	}
	if q.unreadCount() != 0 || len(q.UnreadKnowledge) != 0 {
		t.Fatal("project notifications remained")
	}
	if !enabled.Equal(q.KnowledgeRead.StartedAt) || beforeFailed != q.InboxFailed {
		t.Fatal("clearing changed baseline, messages or task")
	}
	afterOther, _ := os.ReadFile(otherPath)
	if string(beforeOther) != string(afterOther) || otherQ.unreadCount() != 1 {
		t.Fatal("another project was changed")
	}
	a.access.Queues = nil
	reopened := a.projectQueueStateFor(p)
	a.observeKnowledgeCatalog(p, reopened, docs)
	if reopened.unreadCount() != 0 {
		t.Fatal("reopen lost cleared state")
	}
	a.observeKnowledgeCatalog(p, reopened, append(docs, unreadDoc("later")))
	if reopened.unreadCount() != 1 {
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
	if q.unreadCount() != 0 {
		t.Fatal("context menu did not clear the project")
	}
	if a.page != page || a.project != project || a.settingsOrigin != origin {
		t.Fatal("right-click changed the current reading location")
	}
	if !tt.HasText("已清空「memlane」的未读记忆") {
		t.Fatal("success feedback missing")
	}
}

func TestProjectClearNotificationsRejectsCorruptState(t *testing.T) {
	for _, state := range []string{"knowledge", "missing-knowledge"} {
		t.Run(state, func(t *testing.T) {
			a, q, p, _ := notificationMenuFixture(t)
			path, _ := a.knowledgeReadPath(p)
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
			if (state != "missing-knowledge" && string(b) != "corrupt") || (state == "missing-knowledge" && len(b) != 0) || q.unreadCount() != 1 {
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
	if err := a.clearProjectNotifications(q, p); err == nil || q.unreadCount() != 1 || q.InboxFailed == 0 {
		t.Fatal("write failure reported success or cleared state")
	}
}
