package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/app/shell"
)

func TestOfficialReadUsesDatasetAndCredential(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			t.Error("management credential missing")
		}
		if r.Header.Get("X-Api-Key") != "" {
			t.Error("unexpected X-Api-Key header on gateway request")
		}
		switch r.URL.Path {
		case "/api/knowledge":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"project_id":"p","dataset_id":"dataset","items":[{"doc_id":"lesson","dataset_id":"dataset","kind":"knowledge","doc_name":"lesson.md","session_id":"session"}]}`))
		case "/api/raw-records":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"items":[{"kind":"document","doc_id":"raw","dataset_id":"dataset","doc_name":"raw.md"}]}`))
		case "/api/knowledge/dataset/lesson/body":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"dataset_id":"dataset","document_id":"lesson","content":"# Actual official content","mime_type":"text/markdown"}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	client := Client{Config: Config{ProjectControl: readingControlConfig(t, server.URL)}}
	items, _, e := client.documents(context.Background(), Project{ID: "p", Dataset: "dataset"}, 0, false)
	if e != nil || len(items) != 1 || items[0].ID != "lesson" || items[0].Source != "session" {
		t.Fatalf("%+v %v", items, e)
	}
	i, e := client.read(context.Background(), items[0])
	if e != nil || i.Body != "# Actual official content" {
		t.Fatalf("%+v %v", i, e)
	}
	raw, _, e := client.documents(context.Background(), Project{ID: "p", Dataset: "dataset"}, 0, true)
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
		if payload["semantic"] != true || payload["q"] != "stock" {
			t.Errorf("unexpected query %+v", payload)
		}
		_, _ = w.Write([]byte(`{"ok":true,"items":[],"semantic_hits":[{"dataset_id":"dataset","document_id":"source-id","title":"actual.md","snippet":"real passage"}]}`))
	}))
	defer server.Close()
	rows, e := (Client{Config: Config{ProjectControl: readingControlConfig(t, server.URL)}}).search(context.Background(), []Project{{Dataset: "dataset"}}, "stock")
	if e != nil || len(rows) != 1 || rows[0].SourceDocument != "source-id" || rows[0].ID != "source-id" {
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
		case "/v1/datasets/d/documents/doc":
			_, _ = w.Write([]byte(`{"id":"doc","datasetId":"d","name":"lesson","externalMetadata":{"node_set":["session_learnings","session_learnings:session"]},"session_id":"session"}`))
		case "/api/knowledge/d/doc/body":
			_, _ = w.Write([]byte(`{"dataset_id":"d","document_id":"doc","content":"# Actual lesson","mime_type":"text/markdown"}`))
		case "/api/datasets/d/sessions/session":
			_, _ = w.Write([]byte(`{"session_id":"session","dataset_id":"d","user_id":"owner"}`))
		case "/v1/raw-records/session-history":
			_, _ = w.Write([]byte(`{"session_id":"session","dataset_id":"d","user_id":"owner","history_paging":true,"page_limit":20,"qas":[{"qa_id":"qa","question":"actual question","answer":"actual answer"}],"msg_count":1}`))
		case "/v1/provenance/backfill-legacy-dates", "/v1/knowledge/recalls":
			w.WriteHeader(404)
		default:
			t.Errorf("unexpected read %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	a := New(Config{ProjectControl: readingControlConfig(t, server.URL)}, nil)
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
	s := &pane{Selected: &chunk, Query: "query to retain", DetailScroll: map[string]*ui.ScrollState{chunk.Key(): {Y: 75}}}
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
	if s.Selected.ID != "chunk" || len(s.History) != 0 || s.Query != "query to retain" || s.DetailScroll[chunk.Key()].Y != 75 {
		t.Fatal("lost original search context")
	}
}

func TestNativeFirstCodeEnableUsesOfficialDatasetAndOneBlockingIndex(t *testing.T) {
	creates, indexes := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/projects/project/code-activity":
			_, _ = w.Write([]byte(`{"project_id":"project","code_dataset_id":"code-dataset","active":false,"status":""}`))
		case "/v1/projects/project/code-dataset":
			creates++
			_, _ = w.Write([]byte(`{"id":"code-dataset","name":"project-code","project_id":"project"}`))
		case "/v1/projects/project/code-index":
			indexes++
			var payload map[string]string
			_ = json.NewDecoder(r.Body).Decode(&payload)
			if payload["repo_path"] != "/actual/project" {
				t.Errorf("unexpected code-index payload %+v", payload)
			}
			_, _ = w.Write([]byte(`{"status":"completed","dataset_id":"code-dataset","pipeline_run_id":"official-run","items_processed":1}`))
		default:
			t.Errorf("unexpected route %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	project := Project{ID: "project", Name: "Project", Path: "/actual/project", Dataset: "knowledge-dataset"}
	c := Client{Config: Config{ProjectControl: readingControlConfig(t, server.URL), Projects: []Project{project}}}
	ds, err := c.ensureCodeDataset(context.Background(), project)
	if err != nil || ds != "code-dataset" {
		t.Fatalf("ensureCodeDataset: ds=%q err=%v", ds, err)
	}
	project.CodeDataset = ds
	project.CodeEnabled = true
	res, err := c.indexProject(context.Background(), project)
	if err != nil || creates != 1 || indexes != 1 || res.Status != "completed" || res.Dataset != "code-dataset" {
		t.Fatalf("create/index ownership incorrect: creates=%d indexes=%d res=%+v err=%v", creates, indexes, res, err)
	}
}

func TestOfficialActiveIndexPreventsNewSubmission(t *testing.T) {
	posts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			posts++
		}
		if r.URL.Path == "/v1/projects/p/code-activity" {
			_, _ = w.Write([]byte(`{"project_id":"p","code_dataset_id":"code","active":true,"status":"running","run_id":"running-op"}`))
			return
		}
		w.WriteHeader(404)
	}))
	defer server.Close()
	_, err := (Client{Config: Config{ProjectControl: readingControlConfig(t, server.URL)}}).indexProject(context.Background(), Project{ID: "p", CodeDataset: "code"})
	if err != errOfficialCodeActive || posts != 0 {
		t.Fatalf("active official operation resubmitted: %v, posts=%d", err, posts)
	}
}

func TestRecreatedOfficialDatasetDoesNotAdoptHistoricalRun(t *testing.T) {
	cases := []struct {
		name            string
		codeDatasetID   string
		active          bool
		status          string
		httpStatus      int
		wantErr         bool
		wantActiveState bool
	}{
		{"inactive-historical", "code", false, "DATASET_PROCESSING_COMPLETED", 200, false, false},
		{"active-current", "code", true, "running", 200, false, true},
		{"mismatched-dataset", "other-code", true, "running", 200, true, false},
		{"not-configured", "", false, "", 409, true, false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/projects/p/code-activity" {
					w.WriteHeader(404)
					return
				}
				if test.httpStatus != 200 {
					w.WriteHeader(test.httpStatus)
					_, _ = w.Write([]byte(`{"error":"code_dataset_not_configured"}`))
					return
				}
				_ = json.NewEncoder(w).Encode(codeActivityV2Response{
					ProjectID: "p", CodeDatasetID: test.codeDatasetID, Active: test.active, Status: test.status, RunID: "run",
				})
			}))
			defer server.Close()
			state, err := (Client{Config: Config{ProjectControl: readingControlConfig(t, server.URL)}}).codeActivity(context.Background(), Project{ID: "p", CodeDataset: "code"})
			if (err != nil) != test.wantErr || !test.wantErr && state.Active != test.wantActiveState {
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
