package simpledesktop

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func sessionDriftFixture(t *testing.T, control *ProjectControlConfig) (*App, *pane) {
	t.Helper()
	a := New(Config{
		ProjectControl: control,
		Projects: []Project{
			{ID: "proj-item", Name: "item-fe", Path: "/synthetic/prod/item-fe", Dataset: "ds-item"},
			{ID: "proj-memlane", Name: "memlane", Path: "/synthetic/AI/memlane", Dataset: "ds-memlane"},
		},
	}, nil)
	a.Update = func(fn func()) { fn() }
	a.access.Next = time.Now().Add(time.Hour)
	a.page, a.kind = "知识", "知识"
	a.service = "已连接"

	s := a.state()
	s.Loaded = true
	s.Knowledge.Initialized = true
	s.Knowledge.CountsLoaded = true
	d1 := Document{
		ID:      "doc-1",
		Dataset: "ds-item",
		Created: "2026-11-21T03:00:00Z",
		Metadata: map[string]any{
			"node_set":    []any{"session_learnings:sess-cross"},
			"lesson_type": "code_location",
			"session_id":  "sess-cross",
		},
	}
	d2 := Document{
		ID:      "doc-2",
		Dataset: "ds-item",
		Created: "2026-11-21T02:00:00Z",
		Metadata: map[string]any{
			"node_set":    []any{"session_learnings:sess-other"},
			"lesson_type": "state",
			"session_id":  "sess-other",
		},
	}
	i1 := Item{
		ID:           d1.ID,
		Dataset:      d1.Dataset,
		Kind:         "知识",
		Title:        "memlane session-context-lookup 说明",
		Body:         "memlane session-context-lookup 说明",
		BodyLoaded:   true,
		ReadingState: "ready",
		Date:         "2026-11-21",
		Source:       "sess-cross",
		Document:     &d1,
	}
	i2 := Item{
		ID:           d2.ID,
		Dataset:      d2.Dataset,
		Kind:         "知识",
		Title:        "item-fe 本地构建说明",
		Body:         "item-fe 本地构建说明",
		BodyLoaded:   true,
		ReadingState: "ready",
		Date:         "2026-11-21",
		Source:       "sess-other",
		Document:     &d2,
	}
	s.Knowledge.Catalog = []Item{i2, i1}
	s.Items = []Item{i2, i1}
	s.Selected = &s.Items[0]
	return a, s
}

func TestProjectControlClientSessionDriftEndpoints(t *testing.T) {
	tokenFile := filepath.Join(t.TempDir(), "mgmt.token")
	if err := os.WriteFile(tokenFile, []byte(strings.Repeat("m", 40)), 0o600); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	dismissed := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+strings.Repeat("m", 40) {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/projects/proj-item/session-drift":
			items := []SessionDriftItem{}
			if !dismissed {
				items = append(items, SessionDriftItem{
					SessionID:         "sess-cross",
					BoundProjectID:    "proj-item",
					BoundProjectName:  "item-fe",
					BoundDatasetID:    "ds-item",
					TargetProjectID:   "proj-memlane",
					TargetProjectName: "memlane",
					TargetDatasetID:   "ds-memlane",
					TargetWriteCount:  2,
					TargetReadCount:   1,
					Reason:            "write_drift",
				})
			}
			_ = json.NewEncoder(w).Encode(sessionDriftResponse{StartedAt: "2026-11-21T00:00:00Z", Items: items})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/projects/proj-item/session-drift/dismiss":
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, `{"error":"bad_json"}`, http.StatusBadRequest)
				return
			}
			if body["session_id"] != "sess-cross" || body["target_project_id"] != "proj-memlane" {
				http.Error(w, `{"error":"unexpected_payload"}`, http.StatusBadRequest)
				return
			}
			dismissed = true
			_ = json.NewEncoder(w).Encode(sessionDriftResponse{StartedAt: "2026-11-21T00:00:00Z", Items: []SessionDriftItem{}})
		default:
			http.Error(w, `{"error":"not_found"}`, http.StatusNotFound)
		}
	}))
	defer server.Close()

	client := ProjectControlClient{Config: ProjectControlConfig{URL: server.URL, ManagementTokenFile: tokenFile}}
	items, err := client.projectSessionDrifts(context.Background(), "proj-item")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].TargetProjectName != "memlane" || items[0].TargetWriteCount != 2 {
		t.Fatalf("unexpected drift items: %+v", items)
	}

	after, err := client.dismissSessionDrift(context.Background(), "proj-item", "sess-cross", "proj-memlane")
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 0 {
		t.Fatalf("expected empty items after dismiss, got %+v", after)
	}
}

func TestKnowledgePageSessionDriftBannerLocateDismissAndDetailNote(t *testing.T) {
	tokenFile := filepath.Join(t.TempDir(), "mgmt.token")
	if err := os.WriteFile(tokenFile, []byte(strings.Repeat("m", 40)), 0o600); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	dismissed := false
	dismissCh := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/projects/proj-item/session-drift":
			items := []SessionDriftItem{}
			if !dismissed {
				items = append(items, SessionDriftItem{
					SessionID:          "sess-cross",
					BoundProjectID:     "proj-item",
					BoundProjectName:   "item-fe",
					BoundDatasetID:     "ds-item",
					TargetProjectID:    "proj-memlane",
					TargetProjectName:  "memlane",
					TargetDatasetID:    "ds-memlane",
					TargetWriteCount:   3,
					TargetCommandCount: 1,
					Reason:             "write_drift",
				})
			}
			_ = json.NewEncoder(w).Encode(sessionDriftResponse{StartedAt: "2026-11-21T00:00:00Z", Items: items})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/projects/proj-item/session-drift/dismiss":
			dismissed = true
			_ = json.NewEncoder(w).Encode(sessionDriftResponse{StartedAt: "2026-11-21T00:00:00Z", Items: []SessionDriftItem{}})
			select {
			case dismissCh <- struct{}{}:
			default:
			}
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{}})
		}
	}))
	defer server.Close()

	// Without drift, banner must not render.
	noDriftApp, _ := sessionDriftFixture(t, nil)
	noDriftTT := ui.NewTester(noDriftApp.View, 1200, 820)
	if noDriftTT.HasText("疑似跨项目会话") {
		t.Fatal("banner appeared without drift")
	}

	// With control server returning drift, wait for async load and test UI.
	control := &ProjectControlConfig{URL: server.URL, ManagementTokenFile: tokenFile}
	a, s := sessionDriftFixture(t, control)
	p, _ := a.current()
	tt := ui.NewTester(a.View, 1200, 820)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		tt.Frame()
		if tt.HasText("疑似跨项目会话 · 主要操作了「memlane」") {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}
	if !tt.HasText("疑似跨项目会话 · 主要操作了「memlane」") {
		t.Fatal("drift banner did not render after async fetch")
	}
	if tt.HasText("提示：该会话主要操作路径指向「memlane」") {
		t.Fatal("detail note rendered for unrelated session")
	}

	// Locate drifted session memory and check detail source warning note.
	clickProjectUI(t, tt, "定位相关记忆 (1)")
	tt.Frame()
	if s.Selected == nil || s.Selected.ID != "doc-1" {
		t.Fatalf("expected doc-1 selected after locate, got %+v", s.Selected)
	}
	if !tt.HasText("提示：该会话主要操作路径指向「memlane」，当前仍保存在「item-fe」") {
		t.Fatal("expected detail note on drifted session memory")
	}

	// Dismiss drift and verify banner disappears.
	clickProjectUI(t, tt, "留在当前项目")
	select {
	case <-dismissCh:
	case <-time.After(2 * time.Second):
		t.Fatal("dismiss request not sent")
	}
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		tt.Frame()
		if !tt.HasText("疑似跨项目会话") {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}
	if tt.HasText("疑似跨项目会话") {
		t.Fatal("drift banner remained visible after dismiss")
	}
	if st := a.sessionDriftStateFor(p); st == nil || len(st.Items) != 0 {
		t.Fatalf("expected empty drift state after dismiss, got %+v", st)
	}
}
