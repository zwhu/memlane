package simpledesktop

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestArchivePendingKeepsUnknownAndNewEntriesVisible(t *testing.T) {
	a, q := projectQueueFixture()
	a.Config.ClientStateFile = filepath.Join(t.TempDir(), "client-state.json")
	p := a.access.Directory.Projects[0]
	row := q.Sessions[1]
	oldID, newID := strings.Repeat("a", 64), strings.Repeat("b", 64)
	row.View.Status = PendingMaterials{Pending: 3, Unknown: 2, UnknownEntryIDs: []string{oldID, oldID}}
	a.archivePending(q, p, row)
	if q.ArchiveError != "" {
		t.Fatal(q.ArchiveError)
	}
	if s := q.activePending(row); s.Pending != 1 || s.Unknown != 0 {
		t.Fatal(s)
	}
	if row.View.Status.Pending != 3 || row.View.Status.Unknown != 2 {
		t.Fatal("archiving changed underlying unknown state")
	}
	// Same entry IDs with greater multiplicity, plus a new ID, are new anomalies.
	row.View.Status = PendingMaterials{Pending: 5, Unknown: 4, UnknownEntryIDs: []string{oldID, oldID, oldID, newID}}
	if s := q.activePending(row); s.Pending != 3 || s.Unknown != 2 {
		t.Fatal("new anomalies hidden", s)
	}
	reloaded := &projectQueueState{}
	reloaded.Archives, reloaded.ArchiveError = a.readPendingArchives(p)
	if s := reloaded.activePending(row); s.Pending != 3 || s.Unknown != 2 {
		t.Fatal("archive lost on reload", s)
	}
	// Unrelated policy revision updates must not resurrect archived reminders.
	a.Client.PolicyRevision = "new-policy"
	if rows, message := a.readPendingArchives(p); message != "" || len(rows) != 1 {
		t.Fatal("policy change lost archive", message)
	}
	otherProject := p
	otherProject.Dataset = "another-dataset"
	if rows, _ := a.readPendingArchives(otherProject); len(rows) != 0 {
		t.Fatal("archive crossed dataset identity")
	}
	other := row
	other.Binding.HostSessionKey = "other"
	if reloaded.activePending(other).Unknown != 4 {
		t.Fatal("archive crossed session identity")
	}
	path, _ := a.pendingArchivePath(p)
	b, _ := os.ReadFile(path)
	if strings.Contains(string(b), "statement") || strings.Contains(string(b), "body") {
		t.Fatal("archive copied content")
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatal(info.Mode())
	}
	os.WriteFile(path, []byte("broken"), 0600)
	reloaded.Archives, reloaded.ArchiveError = a.readPendingArchives(p)
	if reloaded.ArchiveError == "" || reloaded.activePending(row).Unknown != 4 {
		t.Fatal("corrupt archive silently hid anomalies")
	}
}

func TestArchivePendingUIEndsReminderAndOffersHistory(t *testing.T) {
	a, q := projectQueueFixture()
	a.Config.ClientStateFile = filepath.Join(t.TempDir(), "client-state.json")
	a.Update = nil
	q.Loaded = true
	q.Next = time.Now().Add(time.Hour)
	q.BacklogNext = q.Next
	p := a.access.Directory.Projects[0]
	q.Documents = nil
	row := q.Sessions[1]
	row.View.Status = PendingMaterials{Pending: 1, Unknown: 1, UnknownEntryIDs: []string{strings.Repeat("a", 64)}}
	tt := ui.NewTester(func(c *ui.Context) { ui.Column(c).Fill().Children(func() { a.projectQueueSection(c, p) }) }, 1200, 820)
	if !tt.HasText("归档此异常") || tt.HasText("重试检查") {
		t.Fatal("unresolvable original write is offered another retry")
	}
	clickProjectUI(t, tt, "归档此异常")
	if q.ArchiveError != "" || len(q.Archives) != 1 {
		t.Fatal(q.ArchiveError)
	}
	if tt.HasText("归档此异常") || !tt.HasText("历史保存异常 · 1 个会话") || !tt.HasText("2 条待保存 · 0 条保存状态待确认") {
		t.Fatal("archive did not leave active queue")
	}
	if label, _ := a.sidebarQueueBadge(a.Config.Projects[0]); label != "" {
		t.Fatal("normal saved queue should not raise an anomaly", label)
	}
	clickProjectUI(t, tt, "历史保存异常 · 1 个会话")
	if !tt.HasText("已归档的记录仍无法确认是否保存") || !tt.HasText("保存结果未知") {
		t.Fatal("history claims success")
	}
	fixtureImage(t, tt, "pending-anomaly-history")
	row.View.Status.UnknownEntryIDs = append(row.View.Status.UnknownEntryIDs, strings.Repeat("b", 64))
	row.View.Status.Pending++
	row.View.Status.Unknown++
	tt.Frame()
	if !tt.HasText("归档此异常") {
		t.Fatal("new unknown in archived session never reappeared")
	}
}

func TestArchivePendingWriteFailurePreservesReminder(t *testing.T) {
	a, q := projectQueueFixture()
	base := filepath.Join(t.TempDir(), "not-a-directory")
	os.WriteFile(base, []byte("protected"), 0600)
	a.Config.ClientStateFile = filepath.Join(base, "client-state.json")
	row := q.Sessions[1]
	row.View.Status.UnknownEntryIDs = []string{strings.Repeat("a", 64)}
	a.archivePending(q, a.access.Directory.Projects[0], row)
	if q.ArchiveError == "" || q.activePending(row).Unknown != 1 || len(q.Archives) != 0 {
		t.Fatal("failed archive removed reminder")
	}
}
