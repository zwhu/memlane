package simpledesktop

import (
	"context"
	"encoding/json"
	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/app/shell"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOfficialReadUsesDatasetAndCredential(t *testing.T) {
	token := filepath.Join(t.TempDir(), "token")
	if e := os.WriteFile(token, []byte("  private-fixture-api-key\n"), 0600); e != nil {
		t.Fatal(e)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") != "private-fixture-api-key" {
			t.Error("credential missing")
		}
		if r.Header.Get("Authorization") != "" {
			t.Error("unexpected Authorization header")
		}
		switch r.URL.Path {
		case "/api/v1/datasets/dataset/data":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[{"id":"lesson","datasetId":"dataset","name":"lesson.md","externalMetadata":{"node_set":["session_learnings","session_learnings:session"]}},{"id":"raw","datasetId":"dataset","name":"raw.md"}]`))
		case "/api/v1/datasets/dataset/data/lesson/raw":
			_, _ = w.Write([]byte("# Actual official content"))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	client := Client{Config: Config{ServiceURL: server.URL, TokenFile: token}}
	items, _, e := client.documents(context.Background(), Project{Dataset: "dataset"}, 0, false)
	if e != nil || len(items) != 1 || items[0].ID != "lesson" || items[0].Source != "session" {
		t.Fatalf("%+v %v", items, e)
	}
	i, e := client.read(context.Background(), items[0])
	if e != nil || i.Body != "# Actual official content" {
		t.Fatalf("%+v %v", i, e)
	}
	raw, _, e := client.documents(context.Background(), Project{Dataset: "dataset"}, 0, true)
	if e != nil || len(raw) != 1 || raw[0].ID != "raw" {
		t.Fatalf("%+v %v", raw, e)
	}
}
func TestNoCredentialForwardToRedirect(t *testing.T) {
	token := filepath.Join(t.TempDir(), "token")
	if e := os.WriteFile(token, []byte("private-fixture-api-key"), 0600); e != nil {
		t.Fatal(e)
	}
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") != "" {
			t.Error("API key forwarded to redirect")
		}
		t.Error("redirect followed")
	}))
	defer other.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") != "private-fixture-api-key" {
			t.Error("credential missing")
		}
		if r.Header.Get("Authorization") != "" {
			t.Error("unexpected Authorization header")
		}
		http.Redirect(w, r, other.URL, 302)
	}))
	defer server.Close()
	_, err := (Client{Config: Config{ServiceURL: server.URL, TokenFile: token}}).request(context.Background(), "GET", "/", nil)
	if err == nil || !strings.Contains(err.Error(), "302") {
		t.Fatal(err)
	}
}
func TestSearchUsesOfficialChunkIdentityAndDocumentReference(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if e := json.NewDecoder(r.Body).Decode(&payload); e != nil {
			t.Fatal(e)
		}
		if payload["search_type"] != "CHUNKS" || payload["query"] != "stock" {
			t.Errorf("unexpected query %+v", payload)
		}
		_, _ = w.Write([]byte(`[{"dataset_id":"dataset","search_result":[{"id":"chunk-id","text":"real passage","document_id":"source-id","document_name":"actual.md"}]}]`))
	}))
	defer server.Close()
	rows, e := (Client{Config: Config{ServiceURL: server.URL}}).search(context.Background(), []Project{{Dataset: "dataset"}}, "stock")
	if e != nil || len(rows) != 1 || rows[0].SourceDocument != "source-id" || rows[0].ID != "chunk-id" {
		t.Fatalf("%+v %v", rows, e)
	}
}
func TestRefreshFailureKeepsReadingAndCount(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer server.Close()
	a := New(Config{ServiceURL: server.URL, Projects: []Project{{ID: "p", Dataset: "d"}}}, nil)
	updates := make(chan func(), 2)
	a.Update = func(fn func()) { updates <- fn }
	a.page = "知识"
	s := a.state()
	s.Loaded = true
	s.Items = []Item{{ID: "keep", Dataset: "d"}}
	s.Selected = &Item{ID: "keep", Dataset: "d", Body: "keep body"}
	a.load(s)
	select {
	case fn := <-updates:
		fn()
	case <-time.After(time.Second):
		t.Fatal("no result")
	}
	if len(s.Items) != 1 || s.Selected.Body != "keep body" || !s.Loaded || s.Error == "" {
		t.Fatalf("refresh damaged reading: %+v", s)
	}
}
func TestLateResultsDoNotOverwriteSwitchedProject(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"id":"old","datasetId":"d","name":"old","externalMetadata":{"node_set":["session_learnings"]}}]`))
	}))
	defer server.Close()
	a := New(Config{ServiceURL: server.URL, Projects: []Project{{ID: "p", Dataset: "d"}}}, nil)
	updates := make(chan func(), 2)
	a.Update = func(fn func()) { updates <- fn }
	a.page = "知识"
	s := a.state()
	a.load(s)
	select {
	case fn := <-updates:
		a.epoch++
		fn()
	case <-time.After(time.Second):
		t.Fatal("no result")
	}
	if len(s.Items) != 0 {
		t.Fatal("late response adopted")
	}
}
func TestOfficialDatasetMismatchRejected(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"id":"wrong","datasetId":"other"}]`))
	}))
	defer server.Close()
	_, _, err := (Client{Config: Config{ServiceURL: server.URL}}).documents(context.Background(), Project{Dataset: "expected"}, 0, true)
	if err == nil {
		t.Fatal("foreign dataset accepted")
	}
}
func TestProjectSavePreservesLauncherConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime.json")
	_ = os.WriteFile(path, []byte(`{"launcher_field":"retain","projects":[]}`), 0600)
	a := New(Config{ConfigFile: path, Projects: []Project{{ID: "p", Dataset: "d", CodeEnabled: true}}}, nil)
	if e := a.saveProjects(); e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), "retain") || !strings.Contains(string(b), `"code_enabled": true`) {
		t.Fatal(string(b))
	}
}
func TestGraphRejectsWrongEndpointsAndDuplicateNodes(t *testing.T) {
	g := &GraphHost{}
	for _, raw := range []string{`{"nodes":[{"id":"same"},{"id":"same"}],"links":[],"color_maps":{}}`, `{"nodes":[{"id":"one"}],"links":[{"source":"one","target":"absent"}],"color_maps":{}}`} {
		if e := g.ApplyRaw(context.Background(), shell.ProjectRef{ID: "p"}, "d", json.RawMessage(raw), nil); e == nil {
			t.Fatal("invalid graph accepted")
		}
	}
}

func TestSupersededGraphLoadCannotRestoreOldProject(t *testing.T) {
	g := &GraphHost{}
	g.Clear()
	g.Clear()
	ticket := uint64(1)
	raw := json.RawMessage(`{"nodes":[{"id":"old-project"}],"links":[],"color_maps":{"node_set":{}}}`)
	if err := g.ApplyRaw(context.Background(), shell.ProjectRef{ID: "old"}, "old-dataset", raw, &ticket); err == nil {
		t.Fatal("stale graph applied")
	}
	if g.HasFrame() || g.Node("old-project") != nil {
		t.Fatal("old project retained")
	}
}

func TestNestedOfficialSourceReturnsToOriginalSearch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/datasets/d/data":
			_, _ = w.Write([]byte(`[{"id":"doc","datasetId":"d","name":"lesson","externalMetadata":{"node_set":["session_learnings","session_learnings:session"]}}]`))
		case "/api/v1/datasets/d/data/doc/raw":
			_, _ = w.Write([]byte("# Actual lesson"))
		case "/api/v1/sessions":
			_, _ = w.Write([]byte(`{"sessions":[{"session_id":"session","dataset_id":"d","user_id":"owner"}],"has_more":false}`))
		case "/api/v1/sessions/session/history":
			_, _ = w.Write([]byte(`{"session_id":"session","dataset_id":"d","user_id":"owner","history_paging":true,"page_limit":20,"qas":[{"qa_id":"qa","question":"actual question","answer":"actual answer"}],"msg_count":1}`))
		default:
			t.Errorf("unexpected read %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	a := New(Config{ServiceURL: server.URL}, nil)
	updates := make(chan func(), 4)
	a.Update = func(fn func()) { updates <- fn }
	step := func() {
		t.Helper()
		select {
		case fn := <-updates:
			fn()
		case <-time.After(time.Second):
			t.Fatal("source did not resolve")
		}
	}
	chunk := Item{ID: "chunk", Dataset: "d", Kind: "知识片段", Body: "actual fragment", SourceDocument: "doc"}
	s := &pane{Selected: &chunk, Query: "query to retain", DetailScroll: map[string]*ui.ScrollState{chunk.key(): {Y: 75}}}
	a.source(s)
	step()
	if s.Selected.ID != "doc" {
		t.Fatal("wrong source document")
	}
	a.source(s)
	step()
	if s.Selected.ID != "session" {
		t.Fatal("wrong official session")
	}
	a.returnFromSource(s)
	step()
	if s.Selected.ID != "doc" || len(s.History) != 1 {
		t.Fatal("lost document return")
	}
	a.returnFromSource(s)
	step()
	if s.Selected.ID != "chunk" || len(s.History) != 0 || s.Query != "query to retain" || s.DetailScroll[chunk.key()].Y != 75 {
		t.Fatal("lost original search context")
	}
}

func TestNativeFirstCodeEnableUsesOfficialDatasetAndOneBlockingIndex(t *testing.T) {
	creates, indexes := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/activity/pipeline-runs":
			_, _ = w.Write([]byte(`[]`))
		case "/api/v1/datasets":
			if r.Method == "GET" {
				_, _ = w.Write([]byte(`[{"id":"code-dataset","createdAt":"2026-11-12T16:00:00Z"}]`))
				return
			}
			creates++
			var payload map[string]string
			_ = json.NewDecoder(r.Body).Decode(&payload)
			if payload["name"] != "project-code" {
				t.Error("wrong project code dataset name")
			}
			_, _ = w.Write([]byte(`{"id":"code-dataset","name":"project-code"}`))
		case "/api/v1/remember":
			indexes++
			_ = r.ParseForm()
			if r.Form.Get("raw_data") != "/actual/project" || r.Form.Get("datasetId") != "code-dataset" || r.Form.Get("content_type") != "code" || r.Form.Get("run_in_background") != "false" || r.Form.Get("index_vectors") != "false" {
				t.Errorf("unexpected official index form %+v", r.Form)
			}
			_, _ = w.Write([]byte(`{"status":"completed","dataset_id":"code-dataset","pipeline_run_id":"official-run","items_processed":1}`))
		default:
			t.Errorf("unexpected route %s", r.URL.Path)
		}
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "runtime.json")
	_ = os.WriteFile(path, []byte(`{"projects":[]}`), 0600)
	project := Project{ID: "project", Name: "Project", Path: "/actual/project", Dataset: "knowledge-dataset"}
	a := New(Config{ServiceURL: server.URL, ConfigFile: path, Projects: []Project{project}}, nil)
	updates := make(chan func(), 4)
	a.Update = func(fn func()) { updates <- fn }
	step := func() {
		t.Helper()
		select {
		case fn := <-updates:
			fn()
		case <-time.After(time.Second):
			t.Fatal("official operation did not finish")
		}
	}
	a.toggleCode(true)
	step()
	step()
	if creates != 1 || indexes != 1 || a.Config.Projects[0].CodeDataset != "code-dataset" || a.graphController.CodeBusy || a.graphController.CodeUnknown || !strings.Contains(a.graphController.CodeStatus, "completed") {
		t.Fatalf("create/index ownership incorrect: %d %d %+v", creates, indexes, a)
	}
}

func TestOfficialActiveIndexPreventsNewSubmission(t *testing.T) {
	posts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/datasets" {
			_, _ = w.Write([]byte(`[{"id":"code","createdAt":"2026-11-12T16:00:00Z"}]`))
			return
		}
		if r.Method == "POST" {
			posts++
		}
		_, _ = w.Write([]byte(`[{"id":"row","dataset_id":"code","kind":"operation","operation_name":"remember","pipeline_run_id":"running-op","outcome":null,"ended_at":null,"created_at":"2026-11-12T16:30:00","started_at":"2026-11-12T16:30:00"}]`))
	}))
	defer server.Close()
	_, err := (Client{Config: Config{ServiceURL: server.URL}}).indexProject(context.Background(), Project{CodeDataset: "code"})
	if err != errOfficialCodeActive || posts != 0 {
		t.Fatalf("active official operation resubmitted: %v, posts=%d", err, posts)
	}
}

func TestRecreatedOfficialDatasetDoesNotAdoptHistoricalRun(t *testing.T) {
	const generation = "2026-11-12T17:44:03.114200Z"
	cases := []struct {
		name, created, started string
		active, wantErr        bool
	}{
		{"previous-incarnation", "2026-11-12T16:56:21.061337", "2026-11-12T16:56:21.061337", false, false},
		{"equal-boundary", generation, generation, true, false},
		{"new-incarnation", "2026-11-12T17:44:04.000000Z", "2026-11-12T17:44:04", true, false},
		{"new-row-old-start", "2026-11-12T17:44:04Z", "2026-11-12T16:56:21", true, false},
		{"old-row-current-start", "2026-11-12T16:56:21Z", "2026-11-12T17:44:04Z", true, false},
		{"offset-same-new-time", "2026-11-13T01:44:04+08:00", "2026-11-13T01:44:04+08:00", true, false},
		{"missing-start-old-row", "2026-11-12T16:56:21Z", "", false, false},
		{"missing-row-created", "", "2026-11-12T16:56:21", false, true},
		{"invalid-start", "2026-11-12T16:56:21", "unknown", false, true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v1/datasets" {
					_, _ = w.Write([]byte(`[{"id":"code","createdAt":"` + generation + `"}]`))
					return
				}
				_ = json.NewEncoder(w).Encode([]map[string]any{{"id": "row", "kind": "pipeline", "dataset_id": "code", "pipeline_name": "code_graph_pipeline", "pipeline_run_id": "run", "status": "DATASET_PROCESSING_STARTED", "created_at": test.created, "started_at": test.started}})
			}))
			defer server.Close()
			state, err := (Client{Config: Config{ServiceURL: server.URL}}).codeActivity(context.Background(), Project{CodeDataset: "code"})
			if (err != nil) != test.wantErr || !test.wantErr && state.Active != test.active {
				t.Fatalf("state=%+v err=%v", state, err)
			}
		})
	}
}
func TestDatasetRecreatedDuringActivityReadRemainsUnknown(t *testing.T) {
	gets := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/datasets" {
			gets++
			at := "2026-11-12T17:44:03Z"
			if gets > 1 {
				at = "2026-11-12T17:45:03Z"
			}
			_, _ = w.Write([]byte(`[{"id":"code","createdAt":"` + at + `"}]`))
			return
		}
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()
	_, err := (Client{Config: Config{ServiceURL: server.URL}}).codeActivity(context.Background(), Project{CodeDataset: "code"})
	if err == nil {
		t.Fatal("dataset reincarnation raced with activity read")
	}
}
