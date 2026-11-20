package simpledesktop

import (
	"os"
	"path/filepath"
	"testing"
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
			Sessions: []queuedSession{{
				View: &pendingBufferView{Loaded: true, Status: PendingMaterials{Pending: 1, Unknown: 1}},
			}},
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
	if label, _ := a.sidebarQueueBadge(project); label != "!" {
		t.Fatalf("expected cached sidebar queue badge '!', got %q", label)
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
