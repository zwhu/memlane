package graph

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/app/api"
	"github.com/zwhu/memlane/app/shell"
	kitui "github.com/zwhu/memlane/kit/ui"
)

func TestGraphPayloadFormatsAndLimits(t *testing.T) {
	pinned := `{"nodes":[{"id":"a"},{"id":"b"}],"links":[{"source":"a","target":"b"}],"color_maps":{"node_set":{}}}`
	standard := `{"nodes":[{"id":"a","label":"Alpha"},{"id":"b"}],"edges":[{"source":"a","target":"b","relationship":"calls"}]}`
	for _, raw := range []string{pinned, standard} {
		payload, err := DecodePayload(json.RawMessage(raw))
		if err != nil || len(payload.nodes) != 2 || !strings.Contains(string(payload.raw), `"color_maps"`) || !strings.Contains(string(payload.raw), `"links"`) {
			t.Fatalf("decode %s: %+v %v", raw, payload, err)
		}
	}
	for _, raw := range []string{
		`null`, `{"nodes":`, `{"nodes":[]}`,
		`{"nodes":[{"id":" " }],"edges":[]}`,
		`{"nodes":[{"id":"a"},{"id":"a"}],"edges":[]}`,
		`{"nodes":[{"id":"a"}],"edges":[{"source":"a","target":"missing"}]}`,
		`{"nodes":[{"id":"a"},{"id":"a"}],"links":[],"color_maps":{}}`,
		`{"nodes":[{"id":"x"}],"links":[{"source":"x","target":"y"}],"color_maps":{}}`,
		`{"nodes":[],"links":[],"edges":[],"color_maps":{}}`,
	} {
		if _, err := DecodePayload(json.RawMessage(raw)); err == nil {
			t.Errorf("accepted invalid payload %s", raw)
		}
	}
	if _, err := NormalizeGraphResponse(api.GraphResponse{Nodes: make([]api.GraphNode, maxNodes+1)}); err == nil {
		t.Error("accepted oversized graph")
	}
	if _, err := NormalizeGraphResponse(api.GraphResponse{Edges: make([]api.GraphEdge, maxEdges+1)}); err == nil {
		t.Error("accepted oversized edge list")
	}
	raw := `{"nodes":[` + strings.Repeat(`{"id":"x"},`, maxNodes) + `{"id":"y"}],"links":[],"color_maps":{}}`
	if _, err := DecodePayload(json.RawMessage(raw)); err == nil {
		t.Error("accepted oversized pinned graph")
	}
	resp := api.GraphResponse{Nodes: []api.GraphNode{{ID: "a", Label: "Alpha"}, {ID: "b"}}, Edges: []api.GraphEdge{{Source: "a", Target: "b", Relationship: "calls"}}}
	payload, err := NormalizeGraphResponse(resp)
	if err != nil || !strings.Contains(string(payload.nodes["a"]), `"name":"Alpha"`) || !strings.Contains(string(payload.raw), `"relation":"calls"`) {
		t.Fatalf("lost visualizer labels: %s %v", payload.raw, err)
	}
}

func assetFixture() fstest.MapFS {
	files := fstest.MapFS{}
	digests := map[string]string{}
	for _, name := range []string{"index.html", "app.js", "app.css", "host.css"} {
		files[name] = &fstest.MapFile{Data: []byte(name)}
		digests[name] = fmt.Sprintf("%x", sha256.Sum256(files[name].Data))
	}
	raw, _ := json.Marshal(map[string]any{"protocol": 1, "upstream": "8853378eae5366addb064e758e560a456146ae1a", "files": digests})
	files["assets.json"] = &fstest.MapFile{Data: raw}
	return files
}

func TestValidateAssets(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(fstest.MapFS)
	}{
		{"valid", nil},
		{"hash mismatch", func(f fstest.MapFS) { f["app.js"].Data = []byte("tampered") }},
		{"missing required file", func(f fstest.MapFS) { delete(f, "host.css") }},
		{"disallowed filename", func(f fstest.MapFS) {
			f["assets.json"].Data = []byte(strings.ReplaceAll(string(f["assets.json"].Data), "app.js", "../app.js"))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := assetFixture()
			if tc.change != nil {
				tc.change(files)
			}
			assets, err := ValidateAssets(func(name string) ([]byte, error) { return fs.ReadFile(files, name) })
			if (err == nil) != (tc.change == nil) || err == nil && len(assets) != 4 {
				t.Fatalf("unexpected asset result: %v %v", assets, err)
			}
		})
	}
	var bridge officialGraphBridge
	host := &Host{assets: map[string][]byte{"app.js": []byte("script")}, pageReady: make(chan struct{})}
	bridge.host.Store(host)
	reply := httptest.NewRecorder()
	bridge.serveProtocol(reply, httptest.NewRequest(http.MethodGet, "memlanegraph://graph/app.js", nil))
	if reply.Code != http.StatusNotFound {
		t.Fatal("protocol served assets before publishing a sandboxed page")
	}
}

func awaitGraph[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(2 * time.Second):
		t.Fatal("graph callback stalled")
		var zero T
		return zero
	}
}

func TestHostCoalescesAndFencesSelection(t *testing.T) {
	ctx := context.Background()
	updates, scripts, canceled := make(chan func(), 8), make(chan string, 8), make(chan struct{}, 8)
	release := make(chan struct{})
	host := NewTestHost(func(fn func()) { updates <- fn }, func(ctx context.Context, js string) (any, error) {
		scripts <- js
		<-ctx.Done()
		canceled <- struct{}{}
		<-release
		return nil, ctx.Err()
	})
	t.Cleanup(host.Close)
	project := shell.ProjectRef{ID: "p"}
	raw := json.RawMessage(`{"nodes":[{"id":"a"}],"links":[],"color_maps":{}}`)
	if err := host.ApplyRaw(ctx, project, "old", raw, nil); err != nil {
		t.Fatal(err)
	}
	awaitGraph(t, scripts)
	epoch := host.Epoch()
	if err := host.ApplyRaw(ctx, project, "old", raw, &epoch); err != nil || host.Epoch() != epoch {
		t.Fatalf("identical payload replaced canvas: %v", err)
	}
	host.Clear()
	awaitGraph(t, canceled)
	if err := host.ApplyRaw(ctx, project, "new", raw, &epoch); err == nil {
		t.Fatal("superseded ticket restored a graph")
	}
	if err := host.ApplyRaw(ctx, project, "new", raw, nil); err != nil {
		t.Fatal(err)
	}
	if !host.IsRendering() || !host.HasPending() || len(scripts) != 0 {
		t.Fatal("updates did not coalesce")
	}
	close(release)
	awaitGraph(t, updates)()
	if script := awaitGraph(t, scripts); !strings.Contains(script, `"dataset":"new"`) {
		t.Fatalf("wrong pending render: %s", script)
	}
	got := ""
	host.SetOnSelect(func(raw json.RawMessage) { got = string(raw) })
	for _, selection := range []SelectRequest{
		{epoch, "p", "new", "a"}, {host.Epoch(), "other", "new", "a"},
		{host.Epoch(), "p", "old", "a"}, {host.Epoch(), "p", "new", "missing"},
		{host.Epoch(), "p", "new", "a"},
	} {
		errs := make(chan error, 1)
		go func() { errs <- host.Select(ctx, selection) }()
		awaitGraph(t, updates)()
		if err := awaitGraph(t, errs); (err == nil) != (selection == (SelectRequest{host.Epoch(), "p", "new", "a"})) {
			t.Fatalf("unexpected selection result: %+v %v", selection, err)
		}
	}
	if !strings.Contains(got, `"id":"a"`) {
		t.Fatal("valid selection did not reach inspector")
	}
	host.Close()
	awaitGraph(t, canceled)
	awaitGraph(t, updates)()
	if host.IsRendering() || host.HasPending() {
		t.Fatal("closed graph retained work")
	}
}

type graphFence struct{ epoch uint64 }

func (f *graphFence) Epoch() uint64         { return f.epoch }
func (*graphFence) AuthEpoch() uint64       { return 1 }
func (*graphFence) ReadingIdentity() string { return "test-reader" }
func (f *graphFence) InvalidateReading()    { f.epoch++ }

type graphTheme struct{ dark bool }

func (t graphTheme) Tokens() *kitui.Tokens { return kitui.TokensFor(t.dark) }
func (graphTheme) ReduceMotion() bool      { return true }

type graphTransport func(*http.Request) (*http.Response, error)

func (f graphTransport) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func graphAPI(t *testing.T, handler http.Handler) *api.Client {
	t.Helper()
	cfg := api.Config{BaseURL: "http://127.0.0.1", ReadingIdentity: "test-reader"}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err == nil {
		_ = listener.Close()
		server := httptest.NewServer(handler)
		t.Cleanup(server.Close)
		cfg.BaseURL, cfg.HTTPClient = server.URL, server.Client()
	} else {
		t.Logf("loopback listener unavailable, using recorder transport: %v", err)
		cfg.HTTPClient = &http.Client{Transport: graphTransport(func(req *http.Request) (*http.Response, error) {
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)
			return w.Result(), nil
		})}
	}
	client, err := api.NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestControllerLoadsAPIAndDropsStaleEpoch(t *testing.T) {
	requests := make(chan string, 4)
	client := graphAPI(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dataset := r.URL.Query().Get("dataset_id")
		requests <- dataset
		if r.URL.Path != "/api/graph" || r.URL.Query().Get("project_id") != "p" || r.Header.Get("X-Memlane-Reading-Identity") != "test-reader" {
			t.Errorf("unexpected graph request: %s %v", r.URL, r.Header)
		}
		fmt.Fprintf(w, `{"ok":true,"dataset_id":%q,"nodes":[{"id":"node"}],"edges":[]}`, dataset)
	}))
	updates := make(chan func(), 4)
	fence := &graphFence{}
	p := shell.ProjectRef{ID: "p", DatasetID: "entity", CodeDatasetID: "code", CodeEnabled: true}
	g := New(Deps{Client: client, Fence: fence, Dispatcher: shell.DispatchFunc(func(fn func()) { updates <- fn }),
		CurrentProject: func() (shell.ProjectRef, bool) { return p, true }})
	g.Host = &Host{}
	g.Load()
	if got := awaitGraph(t, requests); got != "entity" {
		t.Fatalf("entity mode requested %q", got)
	}
	fence.epoch++
	awaitGraph(t, updates)()
	if g.Host.HasFrame() || g.Loading {
		t.Fatal("stale graph crossed the session fence")
	}
	g.ResetView(ModeCode)
	g.Load()
	if got := awaitGraph(t, requests); got != "code" {
		t.Fatalf("code mode requested %q", got)
	}
	awaitGraph(t, updates)()
	if g.Host.Dataset() != "code" || g.Host.Node("node") == nil || g.Loading || g.Error != "" {
		t.Fatalf("graph response not applied: %+v", g)
	}
	g.InvalidateReading("entity")
	if !g.Host.HasFrame() {
		t.Fatal("unrelated dataset invalidation cleared code graph")
	}
	g.InvalidateReading("code")
	if g.Host.HasFrame() {
		t.Fatal("invalidated code graph remained visible")
	}
}

func TestGraphViewRendersBothAppearances(t *testing.T) {
	for _, dark := range []bool{false, true} {
		g := New(Deps{Theme: graphTheme{dark}, CurrentProject: func() (shell.ProjectRef, bool) {
			return shell.ProjectRef{ID: "p", DatasetID: "ds"}, true
		}})
		g.Mode = ModeCode
		tt := ui.NewTester(g.RenderView, 1200, 820)
		for _, title := range []string{"实体关系", "代码图谱", "代码图谱尚未开启", "开启代码图谱"} {
			if !tt.HasText(title) {
				t.Errorf("dark=%v missing %q", dark, title)
			}
		}
	}
}
