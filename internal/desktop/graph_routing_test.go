package desktop

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func graphRoutingBorderDistance(n projectGraphNode, p ui.Point, w, h float32) float64 {
	radius := float64(8)
	if n.entity {
		radius = float64(h / 2)
	}
	dx := math.Abs(float64(p.X-n.x)) - float64(w/2) + radius
	dy := math.Abs(float64(p.Y-n.y-h/2)) - float64(h/2) + radius
	return math.Hypot(math.Max(dx, 0), math.Max(dy, 0)) + math.Min(math.Max(dx, dy), 0) - radius
}
func TestGraphRoutingFewDenseAndBoundary(t *testing.T) {
	for _, size := range [][2]float32{{624, 350}, {324, 300}} {
		for _, count := range []int{1, 4, 12} {
			nodes := []projectGraphNode{}
			for i := 0; i < count; i++ {
				nodes = append(nodes, projectGraphNode{id: fmt.Sprint(i), entity: true})
			}
			nodes = append(nodes, projectGraphNode{id: "material"})
			placed, height, w, h := layoutProjectGraph(nodes, size[0], size[1])
			for _, n := range placed {
				if n.x-w/2 < 0 || n.x+w/2 > size[0] || n.y < 0 || n.y+h > height {
					t.Fatal("node outside canvas", n)
				}
				if count <= 4 && n.y+h > size[1] {
					t.Fatal("few-node clipping", n)
				}
			}
			if count > 4 && height <= size[1] {
				t.Fatal("dense graph stopped scrolling")
			}
			from, to := placed[count-1], placed[0]
			q := projectGraphCurve(from, to, w, h, 2, true)
			if math.Abs(graphRoutingBorderDistance(from, q.Start, w, h)) > .02 || math.Abs(graphRoutingBorderDistance(to, q.End, w, h)) > .02 {
				t.Fatal("curve connected to centre instead of boundary", q)
			}
			if count > 1 && q.Label.X >= from.x-w/2 {
				t.Fatal("same-column curve runs through intermediate nodes", q)
			}
			if q.Start == q.End {
				t.Fatal("self curve retraces one straight line")
			}
		}
	}
}
func TestGraphRoutingActualBackend1200And800(t *testing.T) {
	out := os.Getenv("MEMLANE_GRAPH_ROUTING_OUT")
	if out == "" {
		t.Skip("explicit actual synthetic evidence output required")
	}
	load := func(path string, target any) {
		b, e := os.ReadFile(path)
		if e != nil {
			t.Fatal(e)
		}
		if e = json.Unmarshal(b, target); e != nil {
			t.Fatal(e)
		}
	}
	var plan struct {
		Root      string `json:"socket_root"`
		Project   string `json:"project"`
		Dataset   string `json:"dataset"`
		Scratch   string `json:"scratch"`
		Synthetic bool   `json:"synthetic_only"`
	}
	load(os.Getenv("MEMLANE_GRAPH_ROUTING_PLAN"), &plan)
	var marker struct {
		Synthetic bool `json:"synthetic_only"`
	}
	load(filepath.Join(plan.Scratch, "synthetic-only.json"), &marker)
	if !marker.Synthetic || !plan.Synthetic {
		t.Fatal("synthetic root not verified")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	client := serviceipc.Library{Root: plan.Root}
	graph, e := client.CogneeGraph(ctx, records.CogneeGraphQuery{Project: plan.Project, Dataset: plan.Dataset})
	if e != nil {
		t.Fatal(e)
	}
	scopes, e := client.Scopes(ctx)
	if e != nil {
		t.Fatal(e)
	}
	page, e := client.List(ctx, records.ListQuery{Datasets: []string{plan.Project}, Kind: "knowledge", Limit: 50})
	if e != nil {
		t.Fatal(e)
	}
	if len(graph.Entities) != 4 || len(graph.Relations) != 5 {
		t.Fatal("actual graph changed; do not replace it with fixture", len(graph.Entities), len(graph.Relations))
	}
	geometries := map[string]any{}
	for _, width := range []int{1200, 800} {
		a := New("actual backend routing headless, not Native", nil)
		a.visualFixture = true
		a.library = client
		a.guidanceRoot = t.TempDir()
		a.page, a.scope = "项目", plan.Project
		a.scopes = scopes
		for _, s := range scopes {
			if s.ID == plan.Project {
				a.projects = []ProjectItem{{ID: s.ID, Name: s.Name, Path: s.Path}}
			}
		}
		for _, v := range page.Items {
			a.items = append(a.items, displayVersion(v))
		}
		a.selectedID = "abd8c921-efc3-57dc-b3bd-cb52069bc81f"
		a.recordKind = "knowledge"
		a.browsing.Graph = graph
		a.browsing.Project = plan.Project
		a.browsing.View = "relations"
		a.browsing.Dataset = graph.Dataset
		rawNodes, _ := a.projectGraphNodes()
		cw := float32(width) - a.navigationWidth(float32(width)) - 316 - 48
		viewport := float32(350)
		if width == 800 {
			cw = float32(width) - a.navigationWidth(float32(width)) - 216 - 48
			viewport = 300
		}
		cw = min(float32(650), max(float32(260), cw))
		nodes, height, w, h := layoutProjectGraph(rawNodes, cw, viewport)
		points := map[string]projectGraphNode{}
		for _, n := range nodes {
			points[n.id] = n
			if n.y+h > viewport || n.x-w/2 < 0 || n.x+w/2 > cw {
				t.Fatal("actual few-node clipping", n)
			}
		}
		routes := []map[string]any{}
		for i, r := range graph.Relations {
			q := projectGraphCurve(points[r.Source], points[r.Target], w, h, i, true)
			routes = append(routes, map[string]any{"id": r.ID, "label": r.Label, "source": r.Source, "target": r.Target, "start": q.Start, "control": q.Control, "end": q.End, "label_anchor": q.Label, "label_right_limit": q.LabelRight})
		}
		renderedNodes := []map[string]any{}
		for _, n := range nodes {
			renderedNodes = append(renderedNodes, map[string]any{"id": n.id, "label": n.label, "entity": n.entity, "raw": n.raw, "x": n.x, "top": n.y, "right": n.x + w/2, "bottom": n.y + h})
		}
		geometries[fmt.Sprint(width)] = map[string]any{"canvas_width": cw, "viewport_height": viewport, "content_height": height, "node_width": w, "node_height": h, "nodes": renderedNodes, "routes": routes}
		tt := ui.NewTester(a.View, width, 760)
		writeScreenshot(t, tt, fmt.Sprintf("routing-actual-%d.png", width))
		// Every actual entity is a visible clickable node, including the fourth one.
		last := graph.Entities[len(graph.Entities)-1]
		if err := tt.Click("关系图节点：" + last.Name); err != nil {
			t.Fatal(err)
		}
		if a.browsing.View != "entities" || a.browsing.Entity != last.ID {
			t.Fatal("actual node click/context lost")
		}
		a.browseView("relations")
		tt.Frame()
		a.openGraphKnowledge("abd8c921-efc3-57dc-b3bd-cb52069bc81f")
		a.returnToBrowsingExplorer()
		tt.Frame()
		if a.browsing.View != "relations" || a.scope != plan.Project {
			t.Fatal("knowledge return lost actual graph context")
		}
	}
	b, _ := json.MarshalIndent(map[string]any{"classification": "HEADLESS ACTUAL BACKEND DATA — NOT NATIVE", "entities": len(graph.Entities), "relations": len(graph.Relations), "graph": graph, "geometry": geometries, "model_calls": 0, "production_changes": false, "routing": "native Path.QuadTo/StrokePath quadratic curves; arrowheads only for real source→target object relations; mention association curves remain undirected", "full_labels": "original relation labels remain in existing object relationship/source disclosure; canvas labels bounded to lane"}, "", "  ")
	if e = os.WriteFile(filepath.Join(out, "report.json"), b, 0600); e != nil {
		t.Fatal(e)
	}
}

func TestGraphRoutingDenseFixture1200And800(t *testing.T) {
	for _, width := range []int{1200, 800} {
		a := graphPresentationApp()
		a.browsing.View = "relations"
		g := records.CogneeGraph{Project: a.scope}
		for i := 0; i < 12; i++ {
			g.Entities = append(g.Entities, records.CogneeEntity{ID: fmt.Sprintf("fixture-e-%d", i), Name: fmt.Sprintf("密集夹具实体%02d", i)})
		}
		a.items = nil
		for i := 0; i < 16; i++ {
			memory := fmt.Sprintf("fixture-k-%d", i)
			source := records.CogneeGraphSource{Memory: memory, Revision: 1, Quote: "synthetic dense layout only", Origin: "knowledge"}
			g.Mentions = append(g.Mentions, records.CogneeMention{Entity: g.Entities[i%12].ID, CogneeGraphSource: source})
			a.items = append(a.items, MemoryItem{ID: memory, Dataset: a.scope, Title: fmt.Sprintf("密集夹具材料%02d", i), Origin: "knowledge", Revision: 1, State: "active"})
			if i < 12 {
				g.Relations = append(g.Relations, records.CogneeRelation{ID: fmt.Sprint(i), Source: g.Entities[i].ID, Target: g.Entities[(i+2)%12].ID, Label: "fixture_reference", Sources: []records.CogneeGraphSource{source}})
			}
		}
		a.browsing.Graph = g
		nodes, materials := a.projectGraphNodes()
		if len(nodes) != 28 || len(materials) != 16 {
			t.Fatal("existing graph limits changed")
		}
		tt := ui.NewTester(a.View, width, 760)
		writeScreenshot(t, tt, fmt.Sprintf("routing-dense-fixture-%d.png", width))
		if a.browsing.GraphScroll.MaxY <= 0 {
			t.Fatal("dense canvas cannot scroll")
		}
		a.browsing.GraphScroll.Y = a.browsing.GraphScroll.MaxY
		tt.Frame()
		writeScreenshot(t, tt, fmt.Sprintf("routing-dense-fixture-bottom-%d.png", width))
	}
}
