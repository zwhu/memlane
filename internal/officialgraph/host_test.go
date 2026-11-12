package officialgraph

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

type testSource struct {
	mu                    sync.Mutex
	denied                bool
	b                     records.CogneeBinding
	gate, entered         chan struct{}
	authGate, authEntered chan struct{}
}

func (s *testSource) OfficialVisualizationBinding(ctx context.Context, q records.OfficialVisualizationQuery) (records.CogneeBinding, error) {
	s.mu.Lock()
	denied, b, gate, entered := s.denied, s.b, s.authGate, s.authEntered
	s.mu.Unlock()
	if entered != nil {
		select {
		case entered <- struct{}{}:
		default:
		}
	}
	if gate != nil {
		<-gate
	}
	if denied {
		return records.CogneeBinding{}, errors.New("revoked")
	}
	b.Dataset = q.Dataset
	return b, nil
}
func (s *testSource) OfficialVisualization(ctx context.Context, q records.OfficialVisualizationQuery) (records.OfficialVisualization, error) {
	if q.Dataset == "old" && s.gate != nil {
		s.entered <- struct{}{}
		<-s.gate
	}
	return records.OfficialVisualization{Instance: q.Instance, Dataset: q.Dataset, Scope: "bounded_subgraph", MaxNodes: 1000, Payload: json.RawMessage(`{"nodes":[{"id":"selected","name":"Original","stage":"entity","file_path":"not-a-navigation-grant"}],"links":[],"color_maps":{}}`)}, nil
}
func hostFixture(t *testing.T) (*Host, *testSource, records.OfficialVisualizationQuery, *[]string, *sync.Mutex) {
	t.Helper()
	q := records.OfficialVisualizationQuery{Project: uuid.NewString(), Instance: uuid.NewString(), Dataset: uuid.NewString(), MaxNodes: 1000}
	s := &testSource{b: records.CogneeBinding{Operation: uuid.NewString(), Project: q.Project, Instance: q.Instance, Dataset: q.Dataset, Generation: 1}}
	var codes []string
	mu := &sync.Mutex{}
	h := &Host{source: s, ready: true, eval: func(ctx context.Context, code string) (any, error) {
		mu.Lock()
		codes = append(codes, code)
		mu.Unlock()
		return true, nil
	}}
	t.Cleanup(h.Close)
	return h, s, q, &codes, mu
}
func selectionFor(h *Host) Selection {
	h.mu.Lock()
	defer h.mu.Unlock()
	return Selection{Epoch: h.epoch, Project: h.query.Project, Instance: h.query.Instance, Dataset: h.query.Dataset, Node: "selected"}
}

func TestHostScopeSelectionRevocationAndNoPathNavigation(t *testing.T) {
	h, s, q, codes, mu := hostFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := h.Load(ctx, q); err != nil {
		t.Fatal(err)
	}
	called := 0
	h.onSelect = func(_ context.Context, node SelectedNode) error {
		called++
		if !strings.Contains(string(node.Data), "Original") {
			t.Fatal("node data not from frozen payload")
		}
		return nil
	}
	selected := selectionFor(h)
	if err := h.selectNode(ctx, selected); err != nil || called != 1 {
		t.Fatal(err, called)
	}
	for _, bad := range []Selection{{Epoch: selected.Epoch, Project: q.Project, Instance: q.Instance, Dataset: q.Dataset, Node: "/etc/passwd"}, {Epoch: selected.Epoch - 1, Project: q.Project, Instance: q.Instance, Dataset: q.Dataset, Node: "selected"}, {Epoch: selected.Epoch, Project: q.Project, Instance: q.Instance, Dataset: "other", Node: "selected"}} {
		if h.selectNode(ctx, bad) == nil {
			t.Fatal("foreign selection accepted")
		}
	}
	s.mu.Lock()
	s.denied = true
	s.mu.Unlock()
	if h.selectNode(ctx, selected) == nil || called != 1 {
		t.Fatal("revoked callback dispatched")
	}
	deadline := time.Now().Add(3 * time.Second)
	for h.Visible() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if h.Visible() {
		t.Fatal("live grant revocation left graph visible")
	}
	cleared := false
	for time.Now().Before(deadline) {
		mu.Lock()
		last := (*codes)[len(*codes)-1]
		mu.Unlock()
		if strings.Contains(last, ".clear(") {
			cleared = true
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !cleared {
		t.Fatal("revoked graph was not cleared")
	}
}

func TestHostLateLoadAndSelectionCannotReplaceNewScope(t *testing.T) {
	h, s, q, _, _ := hostFixture(t)
	s.gate = make(chan struct{})
	s.entered = make(chan struct{}, 1)
	old := q
	old.Dataset = "old"
	done := make(chan error, 1)
	go func() { done <- h.Load(context.Background(), old) }()
	<-s.entered
	if err := h.Load(context.Background(), q); err != nil {
		t.Fatal(err)
	}
	close(s.gate)
	if err := <-done; err == nil {
		t.Fatal("late old load published")
	}
	if !h.Visible() || selectionFor(h).Dataset != q.Dataset {
		t.Fatal("new scope lost")
	}
	s.mu.Lock()
	s.authGate = make(chan struct{})
	s.authEntered = make(chan struct{}, 1)
	gate, entered := s.authGate, s.authEntered
	s.mu.Unlock()
	called := false
	h.onSelect = func(context.Context, SelectedNode) error { called = true; return nil }
	selected := selectionFor(h)
	go func() { done <- h.selectNode(context.Background(), selected) }()
	<-entered
	h.Clear()
	close(gate)
	if err := <-done; err == nil || called {
		t.Fatal("late authorized selection escaped clear")
	}
}

func TestBundlePinsAllServedBytesAndRefusesUnknownFiles(t *testing.T) {
	dir := t.TempDir()
	hashes := map[string]string{}
	for _, name := range []string{"index.html", "app.js", "app.css", "host.css"} {
		bytes := []byte("synthetic " + name)
		if err := os.WriteFile(filepath.Join(dir, name), bytes, 0600); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(bytes)
		hashes[name] = hex.EncodeToString(sum[:])
	}
	writeManifest := func() {
		raw, _ := json.Marshal(map[string]any{"protocol": 1, "upstream": WebCommit, "files": hashes})
		if err := os.WriteFile(filepath.Join(dir, "assets.json"), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeManifest()
	bundle, err := LoadBundle(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "app.js"), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if string(bundle.files["app.js"]) != "synthetic app.js" {
		t.Fatal("bundle re-read mutable file")
	}
	if _, err := LoadBundle(dir); err == nil {
		t.Fatal("changed script accepted")
	}
	hashes["../private"] = strings.Repeat("0", 64)
	writeManifest()
	if _, err := LoadBundle(dir); err == nil {
		t.Fatal("unknown asset path accepted")
	}
	svc := &pageService{registry: &Registry{hosts: map[*mygo.Page]*Host{}}}
	if svc.Ready(context.Background()) == nil || svc.Select(context.Background(), Selection{}) == nil {
		t.Fatal("missing native CallerPage accepted")
	}
}

func TestHostDoesNotPublishWhenPageRejectsFirstFrame(t *testing.T) {
	h, _, q, _, _ := hostFixture(t)
	h.eval = func(context.Context, string) (any, error) { return false, nil }
	if err := h.Load(context.Background(), q); err == nil || h.Visible() {
		t.Fatal("unacknowledged frame published")
	}
}

func TestHostCompletedLoadDoesNotExpireWithRequestContext(t *testing.T) {
	h, _, q, _, _ := hostFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	if err := h.Load(ctx, q); err != nil {
		t.Fatal(err)
	}
	cancel()
	// A real permission-watch tick must still succeed after the request ends.
	time.Sleep(1100 * time.Millisecond)
	if !h.Visible() {
		t.Fatal("request completion invalidated displayed graph")
	}
	h.Clear()
	if h.Visible() {
		t.Fatal("explicit Clear did not hide graph")
	}
}
