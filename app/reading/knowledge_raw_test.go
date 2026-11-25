package reading

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/zwhu/memlane/app/api"
	"github.com/zwhu/memlane/app/shell"
)

func TestKnowledgeAPIFiltersSortWindowAndRefreshPolicy(t *testing.T) {
	client := readingTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.URL.Path != "/api/knowledge" || q.Get("project_id") != "p" || q.Get("sort") != "recall" ||
			q.Get("category") != "state" || q.Get("unread_only") != "true" ||
			q.Get("recall_agent_id") != "agent" || q.Get("recall_event_id") != "event" || q.Get("limit") != "30" {
			t.Errorf("unexpected knowledge request %s", r.URL)
		}
		_ = json.NewEncoder(w).Encode(api.KnowledgePage{CatalogVersion: "v2", Items: []api.CatalogItem{
			{DocID: "older", Category: "state", IsUnread: true, RecallCount: 5, EffectiveDate: "2026-11-24"},
			{DocID: "newer", Category: "state", IsUnread: true, RecallCount: 3, EffectiveDate: "2026-11-25"},
			{DocID: "read", Category: "state", RecallCount: 10},
			{DocID: "other", Category: "pitfall", IsUnread: true},
		}})
	})
	c := KnowledgeController{KnowledgeDeps: KnowledgeDeps{Client: client}}
	page, err := c.List(t.Context(), api.KnowledgePageOptions{ProjectID: "p", Sort: "recall", Category: "state",
		UnreadOnly: true, RecallAgentID: "agent", RecallEventID: "event"})
	if err != nil || page.CatalogVersion != "v2" {
		t.Fatalf("page: %+v, %v", page, err)
	}
	key := func(i api.CatalogItem) string { return i.DocID }
	keep := FilterKnowledge(page.Items, KnowledgeFilter{"state", true, map[string]bool{"older": true, "newer": true, "read": true}}, key,
		func(i api.CatalogItem) string { return i.Category }, func(i api.CatalogItem) bool { return i.IsUnread })
	SortByRecalls(keep, true, key, func(i api.CatalogItem) string { return i.EffectiveDate }, func(i api.CatalogItem) int64 { return int64(i.RecallCount) })
	if len(keep) != 2 || keep[0].DocID != "older" {
		t.Fatalf("filter/sort: %+v", keep)
	}
	if day, ok := KnowledgeDay(time.FixedZone("UTC+8", 8*3600), "2026-11-25T19:00:00Z", "2026-11-20", "2026-11-21"); !ok || day != "2026-11-20" {
		t.Fatalf("locked reading date = %s", day)
	}
	rows := make([]int, 314)
	for i := range rows {
		rows[i] = i
	}
	head, headDropped := EnforceSlidingWindowDir(rows, false)
	tail, tailDropped := EnforceSlidingWindowDir(rows, true)
	if len(head) != 300 || head[299] != 299 || headDropped[0] != 300 ||
		len(tail) != 300 || tail[0] != 14 || tailDropped[13] != 13 {
		t.Fatal("sliding window displaced the wrong edge")
	}
	if !ShouldSilentRefreshOnCatalogChange(0, false, false) || ShouldSilentRefreshOnCatalogChange(1, false, false) ||
		ShouldSilentRefreshOnCatalogChange(0, true, false) || ShouldSilentRefreshOnCatalogChange(0, false, true) {
		t.Fatal("refresh would disrupt reading")
	}
}

func TestKnowledgeBodyRevocationWithdrawsBeforePublication(t *testing.T) {
	client := readingTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/knowledge/ds/doc":
			_ = json.NewEncoder(w).Encode(api.DocumentDetail{CatalogItem: api.CatalogItem{DocID: "doc", DatasetID: "ds"}})
		case "/api/knowledge/ds/doc/body":
			http.Error(w, "revoked", http.StatusForbidden)
		default:
			http.NotFound(w, r)
		}
	})
	fence := &readingTestFence{}
	queued := make(chan func(), 1)
	controller := KnowledgeController{KnowledgeDeps: KnowledgeDeps{Client: client, Fence: fence,
		Dispatcher: shell.DispatchFunc(func(fn func()) { queued <- fn })}}
	if detail, err := controller.Document(t.Context(), "ds", "doc"); err != nil || detail.DocID != "doc" {
		t.Fatalf("document metadata: %+v %v", detail, err)
	}
	published := false
	controller.Open(t.Context(), "ds", "doc", func(body api.DocumentBody, err error) {
		published = true
		_, copyAllowed := CopyBody(body.Content, false, true)
		if fence.AuthEpoch() != 1 || !Status(err, 403) || copyAllowed || body.Content != "" {
			t.Fatalf("revoked content remained available: %+v %v", body, err)
		}
	})
	take(t, queued)()
	if !published {
		t.Fatal("body withdrawal was not published")
	}
}

func TestRawRecordsAPICursorDedupWindowAndInvalidCursorReload(t *testing.T) {
	queued := make(chan func(), 1)
	fence := &readingTestFence{}
	newSnapshot := false
	client := readingTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		if query.Get("limit") != "30" || query.Get("kind") != "session" {
			t.Errorf("invalid raw request: %s", r.URL)
		}
		if r.URL.Path == "/api/projects/p/raw-records/sessions/root/children" {
			if query.Get("user_id") != "user" {
				t.Error("child scope missing user")
			}
			_ = json.NewEncoder(w).Encode(api.RawRecordsPage{Items: []api.RawItem{{SessionID: "child"}}})
			return
		}
		if r.URL.Path != "/api/raw-records" || query.Get("project_id") != "p" {
			t.Errorf("unexpected raw route: %s", r.URL)
		}
		cursor := query.Get("cursor")
		if cursor == "11" {
			newSnapshot = true
			w.WriteHeader(400)
			_, _ = w.Write([]byte(`{"error":"invalid_cursor"}`))
			return
		}
		var batch int
		_, _ = fmt.Sscan(cursor, &batch)
		out := api.RawRecordsPage{NextCursor: fmt.Sprint(batch + 1), HasMore: !newSnapshot}
		if newSnapshot {
			out.Items = []api.RawItem{{SessionID: "fresh"}}
		} else {
			for i := range 30 {
				out.Items = append(out.Items, api.RawItem{DatasetID: "ds", Kind: "session", SessionID: fmt.Sprint(batch*29 + i)})
			}
		}
		_ = json.NewEncoder(w).Encode(out)
	})
	c := RawRecordsController{Client: client, Fence: fence, Options: api.RawRecordsOptions{ProjectID: "p", Kind: "会话"},
		Dispatcher: shell.DispatchFunc(func(fn func()) { queued <- fn })}
	children, err := c.Page(context.Background(), "root", api.RawRecordsOptions{ProjectID: "p", UserID: "user", Kind: "session"})
	if err != nil || len(children.Items) != 1 || children.Items[0].SessionID != "child" {
		t.Fatalf("children: %+v %v", children, err)
	}
	c.Reload(t.Context())
	take(t, queued)()
	if len(c.Items) != 30 {
		t.Fatalf("initial count: %d", len(c.Items))
	}
	for range 10 {
		c.AutoLoad(t.Context(), 840, 1000)
		take(t, queued)()
	}
	if len(c.Items) != 300 || c.Items[0].SessionID != "20" || c.Items[299].SessionID != "319" {
		t.Fatalf("incorrect raw window: len=%d", len(c.Items))
	}
	c.Append(t.Context())
	take(t, queued)()
	take(t, queued)()
	if c.Busy || len(c.Items) != 1 || c.Items[0].SessionID != "fresh" {
		t.Fatalf("invalid-cursor recovery: %+v", c)
	}
}
