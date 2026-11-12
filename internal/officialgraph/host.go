package officialgraph

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/official"
	"github.com/zwhu/memlane/internal/records"
)

type Source interface {
	OfficialVisualization(context.Context, records.OfficialVisualizationQuery) (records.OfficialVisualization, error)
	OfficialVisualizationBinding(context.Context, records.OfficialVisualizationQuery) (records.CogneeBinding, error)
}

type Selection struct {
	Epoch    uint64 `json:"epoch"`
	Project  string `json:"project"`
	Instance string `json:"instance"`
	Dataset  string `json:"dataset"`
	Node     string `json:"node"`
}

// SelectedNode is an ephemeral official node, never a new knowledge record or
// a trusted path/URL. The destination reader still applies its own permission.
type SelectedNode struct {
	Selection
	Data json.RawMessage
}
type frame struct {
	Epoch    uint64          `json:"epoch"`
	Project  string          `json:"project"`
	Instance string          `json:"instance"`
	Dataset  string          `json:"dataset"`
	Payload  json.RawMessage `json:"payload"`
}

type Host struct {
	mu                     sync.Mutex
	serial                 sync.Mutex
	view                   *mygo.WebView
	registry               *Registry
	source                 Source
	onSelect               func(context.Context, SelectedNode) error
	onChange               func()
	eval                   func(context.Context, string) (any, error)
	epoch                  uint64
	ready, visible, closed bool
	query                  records.OfficialVisualizationQuery
	binding                records.CogneeBinding
	current                *frame
	nodes                  map[string]json.RawMessage
	cancel                 context.CancelFunc
	ackEpoch               uint64
	acked                  bool
}

func (h *Host) Visible() bool { h.mu.Lock(); defer h.mu.Unlock(); return h.visible && !h.closed }
func (h *Host) View(c *ui.Context) {
	if h.Visible() && h.view != nil {
		ui.WebView(c, h.view).Fill()
	}
}

// WebView is the owned native surface, for host capture and integration. Its
// page is not exposed to JavaScript; callers still place it through View.
func (h *Host) WebView() *mygo.WebView { return h.view }

func (h *Host) clearLocked() uint64 {
	h.epoch++
	if h.cancel != nil {
		h.cancel()
		h.cancel = nil
	}
	h.visible = false
	h.current = nil
	h.nodes = nil
	return h.epoch
}

func (h *Host) notify() {
	if h.onChange != nil {
		h.onChange()
	}
}

// serial keeps eval order consistent across requests, revoke, and Ready. JS
// additionally rejects old epochs. No source/authorization work holds it.
func (h *Host) flush(ctx context.Context) error {
	h.serial.Lock()
	defer h.serial.Unlock()
	h.mu.Lock()
	if !h.ready || h.closed {
		h.mu.Unlock()
		return nil
	}
	epoch, current, eval := h.epoch, h.current, h.eval
	h.mu.Unlock()
	var code string
	if current == nil {
		raw, _ := json.Marshal(epoch)
		code = "window.memlaneOfficialGraph.clear(" + string(raw) + ")"
	} else {
		raw, err := json.Marshal(current)
		if err != nil {
			return err
		}
		code = "window.memlaneOfficialGraph.apply(" + string(raw) + ")"
	}
	result, err := eval(ctx, code)
	if err != nil {
		return errors.New("graph_page_update_unconfirmed")
	}
	// A repeated Ready may replay the current accepted epoch. False in that
	// case means JS retained the same state, not a new publication.
	accepted, ok := result.(bool)
	if !ok || !accepted && (!h.acked || h.ackEpoch != epoch) {
		return errors.New("graph_page_update_unconfirmed")
	}
	h.acked = true
	h.ackEpoch = epoch
	return nil
}

func (h *Host) Clear() {
	h.mu.Lock()
	h.clearLocked()
	h.mu.Unlock()
	h.notify()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = h.flush(ctx)
}

func (h *Host) invalidate(epoch uint64) {
	h.mu.Lock()
	current := !h.closed && h.epoch == epoch
	if current {
		h.clearLocked()
	}
	h.mu.Unlock()
	if current {
		h.notify()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = h.flush(ctx)
	}
}

func (h *Host) Load(ctx context.Context, q records.OfficialVisualizationQuery) error {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return errors.New("graph_closed")
	}
	epoch := h.clearLocked()
	// The request context controls loading, not the lifetime of a successfully
	// displayed graph. Clear/Close/next Load own its later permission watch.
	scope, cancel := context.WithCancel(context.Background())
	life, stopLoad := context.WithCancel(ctx)
	stopScope := context.AfterFunc(scope, stopLoad)
	defer func() { stopScope(); stopLoad() }()
	h.cancel = cancel
	h.query = q
	h.mu.Unlock()
	h.notify()
	_ = h.flush(life)
	binding, err := h.source.OfficialVisualizationBinding(life, q)
	var graph records.OfficialVisualization
	if err == nil {
		graph, err = h.source.OfficialVisualization(life, q)
	}
	if err == nil {
		var fresh records.CogneeBinding
		fresh, err = h.source.OfficialVisualizationBinding(life, q)
		if err == nil && fresh != binding {
			err = errors.New("graph_scope_changed")
		}
	}
	var rows struct {
		Nodes []json.RawMessage `json:"nodes"`
	}
	nodes := map[string]json.RawMessage{}
	if err == nil {
		if graph.Instance != q.Instance || graph.Dataset != q.Dataset || graph.Scope != "bounded_subgraph" || graph.MaxNodes != q.MaxNodes || len(graph.Payload) > 8<<20 || official.ValidateVisualizationPayload(graph.Payload, q.MaxNodes) != nil || json.Unmarshal(graph.Payload, &rows) != nil {
			err = errors.New("graph_payload_invalid")
		}
		for _, raw := range rows.Nodes {
			var node struct {
				ID string `json:"id"`
			}
			if json.Unmarshal(raw, &node) != nil || node.ID == "" || len(node.ID) > 256 || nodes[node.ID] != nil {
				err = errors.New("graph_payload_invalid")
				break
			}
			nodes[node.ID] = append(json.RawMessage(nil), raw...)
		}
	}
	h.mu.Lock()
	if h.closed || epoch != h.epoch || life.Err() != nil || scope.Err() != nil {
		h.mu.Unlock()
		cancel()
		return errors.New("graph_load_superseded")
	}
	if err != nil {
		h.clearLocked()
		h.mu.Unlock()
		h.notify()
		return err
	}
	// clear consumed epoch; apply requires a strictly newer epoch.
	h.epoch++
	epoch = h.epoch
	h.binding = binding
	h.nodes = nodes
	h.current = &frame{Epoch: epoch, Project: q.Project, Instance: q.Instance, Dataset: q.Dataset, Payload: append(json.RawMessage(nil), graph.Payload...)}
	h.visible = true
	h.mu.Unlock()
	if err = h.flush(life); err != nil {
		h.invalidate(epoch)
		return err
	}
	h.notify()
	go h.watch(scope, epoch, q, binding)
	return nil
}

func (h *Host) watch(ctx context.Context, epoch uint64, q records.OfficialVisualizationQuery, b records.CogneeBinding) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			h.invalidate(epoch)
			return
		case <-ticker.C:
			probe, stop := context.WithTimeout(ctx, 3*time.Second)
			fresh, err := h.source.OfficialVisualizationBinding(probe, q)
			stop()
			if err != nil || fresh != b {
				h.invalidate(epoch)
				return
			}
		}
	}
}

func (h *Host) selectNode(ctx context.Context, s Selection) error {
	h.mu.Lock()
	valid := !h.closed && h.visible && h.current != nil && s.Epoch == h.epoch && s.Project == h.query.Project && s.Instance == h.query.Instance && s.Dataset == h.query.Dataset && h.nodes[s.Node] != nil
	q, b, data := h.query, h.binding, append(json.RawMessage(nil), h.nodes[s.Node]...)
	h.mu.Unlock()
	if !valid {
		return errors.New("graph_selection_outside_current_scope")
	}
	fresh, err := h.source.OfficialVisualizationBinding(ctx, q)
	if err != nil || fresh != b {
		return errors.New("graph_selection_authorization_changed")
	}
	h.mu.Lock()
	valid = !h.closed && h.visible && s.Epoch == h.epoch
	h.mu.Unlock()
	if !valid {
		return errors.New("graph_selection_superseded")
	}
	if h.onSelect != nil {
		return h.onSelect(ctx, SelectedNode{Selection: s, Data: data})
	}
	return nil
}

func (h *Host) Close() {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	h.clearLocked()
	h.closed = true
	h.mu.Unlock()
	if h.registry != nil && h.view != nil {
		h.registry.mu.Lock()
		delete(h.registry.hosts, h.view.Page())
		h.registry.mu.Unlock()
		h.view.Destroy()
	}
}
