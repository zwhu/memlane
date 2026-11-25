package app

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/zwhu/memlane/app/api"
)

func unreadDoc(id string) Document {
	return Document{ID: id, Dataset: "ds", Name: "记忆 " + id, Created: time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano), Metadata: map[string]any{"node_set": []any{"session_learnings"}, "session_date": "2026-11-16"}}
}

func TestKnowledgeUnreadServerStateAndHumanOpen(t *testing.T) {
	const endpoint = "http://127.0.0.1:16132"
	a, updates := p1TestApp(t, endpoint, endpoint, p1TempToken(t))
	project := a.access.Directory.Projects[0]
	a.Client.HTTP = &http.Client{Transport: queueTestTransport(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/reading-state/mark-read" {
			t.Errorf("unexpected request %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(api.MarkReadResponse{ReadingStateView: api.ReadingStateView{UnreadCount: 0, StateRev: 2}})
	})}
	q := a.projectQueueStateFor(project)
	applyServerUnreadSnapshot(q, 1, 1, false)
	q.InboxFailed = 2
	d := unreadDoc("doc")
	d.Dataset = project.Dataset
	i := Item{ID: d.ID, Dataset: d.Dataset, Kind: "知识", Document: &d, BodyLoaded: true, ServerUnreadKnown: true, ServerUnread: true}
	a.publishOverviewTitle(i)
	if !a.itemIsUnread(i) || q.UnreadCount() != 1 {
		t.Fatal("a background publication marked full content as read")
	}
	i.BodyTruncated = true
	a.markKnowledgeRead(i)
	if !a.itemIsUnread(i) || q.UnreadCount() != 1 {
		t.Fatal("truncated content was marked as read")
	}
	i.BodyTruncated = false
	a.markKnowledgeRead(i)
	a.waitKnowledgeReadFlush()
	drainReading(t, updates, func() bool { return q.ServerUnreadStateRev == 2 })
	if a.itemIsUnread(i) || q.UnreadCount() != 0 || q.InboxFailed != 2 {
		t.Fatal("human open did not clear only the unread knowledge")
	}
}

func TestKnowledgeCatalogVersionWithdrawsOnlyChangedBody(t *testing.T) {
	const endpoint = "http://127.0.0.1:16136"
	a, updates := p1TestApp(t, endpoint, endpoint, p1TempToken(t))
	a.page, a.kind = "知识", "知识"
	s := a.state()
	doc := unreadDoc("doc")
	doc.Dataset, doc.ContentHash = "ds-1", "old"
	s.Selected = &Item{ID: doc.ID, Dataset: doc.Dataset, Kind: "知识", Document: &doc, BodyLoaded: true, Body: "旧正文"}
	s.Loaded, s.Knowledge.Detail = true, true
	s.Knowledge.CatalogVersion = "v1"
	hash, content := "old", "旧正文"
	bodyStarted := make(chan struct{})
	releaseBody := make(chan struct{})
	t.Cleanup(func() { close(releaseBody) })
	a.Client.HTTP = &http.Client{Transport: queueTestTransport(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/knowledge/ds-1/doc":
			_ = json.NewEncoder(w).Encode(api.DocumentDetail{CatalogItem: api.CatalogItem{
				DocID: doc.ID, DatasetID: doc.Dataset, ContentHash: hash,
			}, Content: content})
		case "/api/knowledge/ds-1/doc/body":
			close(bodyStarted)
			<-releaseBody
			_ = json.NewEncoder(w).Encode(api.DocumentBody{DatasetID: doc.Dataset, DocumentID: doc.ID, Content: content})
		default:
			http.NotFound(w, r)
		}
	})}
	apply := func() {
		t.Helper()
		select {
		case fn := <-updates:
			fn()
		case <-time.After(time.Second):
			t.Fatal("catalog version check did not publish")
		}
	}
	a.handleKnowledgeCatalogVersionChanged("p1", doc.Dataset, "v2")
	apply()
	if !s.Knowledge.NewCatalogAvailable || s.Selected == nil || !s.Selected.BodyLoaded || s.Selected.Body != "旧正文" {
		t.Fatal("catalog-only change unmounted the valid selected body")
	}
	hash, content = "new", "新正文"
	a.handleKnowledgeCatalogVersionChanged("p1", doc.Dataset, "v3")
	apply()
	select {
	case <-bodyStarted:
	case <-time.After(time.Second):
		t.Fatal("updated body did not start loading")
	}
	if s.Selected == nil || s.Selected.BodyLoaded || s.Selected.Body != "" {
		t.Fatal("previous version stayed copyable while the new body was loading")
	}
}
