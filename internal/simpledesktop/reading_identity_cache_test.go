package simpledesktop

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBaseReadingIdentityCachedAndSyncedOnTokenChange(t *testing.T) {
	root := t.TempDir()
	tokenPath := filepath.Join(root, "official.token")
	if err := os.WriteFile(tokenPath, []byte("initial-token\n"), 0600); err != nil {
		t.Fatal(err)
	}
	project := Project{ID: "p1", Name: "memlane", Dataset: "ds1"}
	a := New(Config{
		ServiceURL: "http://127.0.0.1:61255",
		TokenFile:  tokenPath,
		Projects:   []Project{project},
	}, nil)
	a.Client.PolicyRevision = "rev-1"

	baseIdentity, err := readingIdentity(Client{Config: a.Client.Config})
	if err != nil {
		t.Fatal(err)
	}
	a.access.Queues = map[string]*projectQueueState{
		baseIdentity + "/" + project.ID + "/" + project.Dataset: {
			Loaded: true,
			KnowledgeRead: knowledgeReadState{
				Read: map[string]bool{"doc-1": false, "doc-2": true},
			},
		},
	}

	if err := os.Remove(tokenPath); err != nil {
		t.Fatal(err)
	}
	if count := a.projectUnread(project); count != 1 {
		t.Fatalf("expected cached unread count 1 after token file removal, got %d", count)
	}
	if !a.itemIsUnread(Item{ID: "doc-1", Dataset: project.Dataset, Kind: "知识"}) {
		t.Fatal("expected cached unread status for doc-1")
	}

	if err := os.WriteFile(tokenPath, []byte("rotated-token\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if a.syncReadingIdentity() {
		t.Fatal("expected syncReadingIdentity to report identity change after token rotation")
	}
	if !a.syncReadingIdentity() {
		t.Fatal("expected subsequent syncReadingIdentity to confirm valid stable token")
	}
	rotatedBase, err := readingIdentity(Client{Config: a.Client.Config})
	if err != nil {
		t.Fatal(err)
	}
	rotatedFull, err := readingIdentity(a.Client)
	if err != nil {
		t.Fatal(err)
	}
	if rotatedBase == baseIdentity {
		t.Fatal("rotated token did not change base reading identity")
	}
	if got := a.cachedBaseReadingIdentity(); got != rotatedBase {
		t.Fatalf("cached base identity %q != expected %q", got, rotatedBase)
	}
	if a.readingIdentity != rotatedFull {
		t.Fatalf("full readingIdentity %q != expected %q", a.readingIdentity, rotatedFull)
	}
}

func TestRenderPathQueueHelpersAvoidSyncTokenAndLocalJSONReads(t *testing.T) {
	root := t.TempDir()
	tokenPath := filepath.Join(root, "official.token")
	if err := os.WriteFile(tokenPath, []byte("cached-token-value\n"), 0600); err != nil {
		t.Fatal(err)
	}
	stateFile := filepath.Join(root, "client-state.json")
	project := Project{ID: "p1", Name: "memlane", Dataset: "ds1"}
	accessProject := AccessProject{ID: "p1", Name: "memlane", Dataset: "ds1"}
	a := New(Config{
		ServiceURL:      "http://127.0.0.1:61255",
		TokenFile:       tokenPath,
		ClientStateFile: stateFile,
		Projects:        []Project{project},
	}, nil)
	a.access.Loaded = true
	a.access.Directory.Projects = []AccessProject{accessProject}

	// A persisted read state would mark the queue as initialized if loaded synchronously.
	readPath, err := a.knowledgeReadPath(accessProject)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeKnowledgeState(readPath, knowledgeReadState{Initialized: true, Read: map[string]bool{"existing": true}}); err != nil {
		t.Fatal(err)
	}

	// Remove token file after initial cache population: render-path helpers must use cachedBaseReadingIdentity
	// and never synchronously read local JSON state files on missing queue entries.
	if err := os.Remove(tokenPath); err != nil {
		t.Fatal(err)
	}
	if alerts := a.trayProjectAlerts(); len(alerts) != 0 {
		t.Fatalf("expected 0 tray alerts for unloaded queue without sync disk reads, got %+v", alerts)
	}
	if progress := a.trayProjectProgressItems(); len(progress) != 0 {
		t.Fatalf("expected 0 tray progress for unloaded queue, got %+v", progress)
	}
	if a.existingProjectQueueState(accessProject) != nil {
		t.Fatal("tray read paths unexpectedly allocated queue state on cache miss")
	}

	// projectQueueStateForAsync allocates without reading local JSON synchronously.
	q := a.projectQueueStateForAsync(accessProject)
	if q == nil || q.KnowledgeRead.Initialized || q.localStateLoaded {
		t.Fatalf("projectQueueStateForAsync unexpectedly read disk synchronously: %+v", q)
	}
	q.Next = time.Now().Add(time.Hour)
	a.refreshSidebarQueueIfDue(time.Now())
	if q.localStateLoaded || q.KnowledgeRead.Initialized {
		t.Fatal("refreshSidebarQueueIfDue performed synchronous local JSON read")
	}

	// Meanwhile syncReadingIdentity must immediately detect the removed token file via readCachedToken.
	if a.syncReadingIdentity() {
		t.Fatal("expected syncReadingIdentity to detect removed token file")
	}
	if a.readingIdentity != "unavailable" {
		t.Fatalf("expected readingIdentity=unavailable after token removal, got %q", a.readingIdentity)
	}
}
