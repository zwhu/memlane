package simpledesktop

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func unreadDoc(id string) Document {
	return Document{ID: id, Dataset: "ds", Name: "记忆 " + id, Created: time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano), Metadata: map[string]any{"node_set": []any{"session_learnings"}, "session_date": "2026-11-16"}}
}
func TestKnowledgeUnreadBaselineReadPersistenceAndIsolation(t *testing.T) {
	a, q := projectQueueFixture()
	a.Config.ClientStateFile = filepath.Join(t.TempDir(), "client-state.json")
	p := a.access.Directory.Projects[0]
	old, fresh := unreadDoc("old"), unreadDoc("new")
	a.observeKnowledgeCatalog(p, q, []Document{old})
	if q.unreadCount() != 0 || !q.KnowledgeRead.Initialized {
		t.Fatal("existing knowledge was called unread")
	}
	lateOld := unreadDoc("late-old")
	lateOld.Created = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
	a.observeKnowledgeCatalog(p, q, []Document{old, lateOld})
	if q.unreadCount() != 0 {
		t.Fatal("late enumeration of pre-enable knowledge was called new")
	}
	a.observeKnowledgeCatalog(p, q, []Document{old, fresh})
	if q.unreadCount() != 1 {
		t.Fatal("new knowledge not counted", q.KnowledgeReadError)
	}
	a.observeKnowledgeCatalog(p, q, []Document{old, fresh})
	if q.unreadCount() != 1 {
		t.Fatal("background catalog read marked knowledge read")
	}
	i := Item{ID: fresh.ID, Dataset: fresh.Dataset, Kind: "知识", Document: &fresh}
	a.publishOverviewTitle(i)
	if q.unreadCount() != 1 {
		t.Fatal("not-yet-loaded title marked read")
	}
	i.BodyLoaded = true
	a.publishOverviewTitle(i)
	if q.unreadCount() != 0 {
		t.Fatal("opening knowledge did not mark read")
	}
	a.Client.PolicyRevision = "updated"
	reload := &projectQueueState{}
	a.observeKnowledgeCatalog(p, reload, []Document{old, fresh})
	if reload.unreadCount() != 0 || !reload.KnowledgeRead.Read[fresh.ID] {
		t.Fatal("restart/revision lost read state")
	}
	a.observeKnowledgeCatalog(p, q, []Document{old, fresh, unreadDoc("newer")})
	a.observeKnowledgeCatalog(p, q, []Document{old, fresh})
	if q.unreadCount() != 0 {
		t.Fatal("deleted knowledge remained unread")
	}
	path, _ := a.knowledgeReadPath(p)
	before, _ := os.ReadFile(path)
	wrong := unreadDoc("foreign")
	wrong.Dataset = "other"
	a.observeKnowledgeCatalog(p, q, []Document{wrong})
	after, _ := os.ReadFile(path)
	if string(before) != string(after) || q.KnowledgeReadError == "" {
		t.Fatal("foreign dataset overwrote read state")
	}
	os.WriteFile(path, []byte("corrupt"), 0600)
	i.ID = "newer"
	a.markKnowledgeRead(i)
	if b, _ := os.ReadFile(path); string(b) != "corrupt" {
		t.Fatal("corrupt state replaced with invented read baseline")
	}
}
func TestUnreadBadgeAndListAreDistinctFromPendingWork(t *testing.T) {
	a, q := projectQueueFixture()
	// This scenario covers ordinary unsent work; confirmed unknown writes now notify immediately.
	q.Sessions[1].View.Status.Unknown = 0
	a.Update = nil
	a.Config.ClientStateFile = filepath.Join(t.TempDir(), "client-state.json")
	p := a.access.Directory.Projects[0]
	q.Next = time.Now().Add(time.Hour)
	q.BacklogNext = q.Next
	a.observeKnowledgeCatalog(p, q, []Document{unreadDoc("old")})
	d := unreadDoc("new")
	a.observeKnowledgeCatalog(p, q, []Document{unreadDoc("old"), d})
	a.page = "知识"
	a.kind = "知识"
	s := a.state()
	s.Items = []Item{{ID: d.ID, Dataset: d.Dataset, Kind: "知识", Title: d.Name, Document: &d}}
	s.Selected = nil
	s.Loaded = true
	s.Error = ""
	s.Loading = false
	tt := ui.NewTester(a.View, 1200, 820)
	if !tt.HasText("未读记忆：memlane") || !tt.HasText("未读") {
		t.Fatal("unread badge/list marker absent")
	}
	if tt.HasText("待处理文档：memlane") {
		t.Fatal("normal pending state notified")
	}
	fixtureImage(t, tt, "project-knowledge-unread")
	i := s.Items[0]
	i.BodyLoaded = true
	a.publishOverviewTitle(i)
	tt.Frame()
	if tt.HasText("未读记忆：memlane") || tt.HasText("未读") {
		t.Fatal("read item remained unread")
	}
}

func TestProjectModelFailureAppearsWithoutChangingSavedMessageState(t *testing.T) {
	a, q := projectQueueFixture()
	q.ProcessingNotices = []processingNotice{{Operation: "original-operation", Status: 502, Code: "codex_model_failed", Backend: "codex"}}
	p := a.access.Directory.Projects[0]
	_, info := a.messageNotices(q, p)
	found := false
	for _, n := range info {
		if n.Key == "processing/original-operation" {
			found = n.Title == "记忆提炼调用失败"
		}
	}
	if !found {
		t.Fatal("model error was hidden behind message-save status")
	}
}
