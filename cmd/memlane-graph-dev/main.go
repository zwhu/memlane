//go:build memlane_graph_e2e

// Development-only native surface check. No daemon, database, Agent, model or
// installed helper is created or controlled by this command.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/officialgraph"
	"github.com/zwhu/memlane/internal/records"
)

type fixtureSource struct {
	mu      sync.Mutex
	revoked bool
	b       records.CogneeBinding
	graph   records.OfficialVisualization
}

func (s *fixtureSource) OfficialVisualization(ctx context.Context, q records.OfficialVisualizationQuery) (records.OfficialVisualization, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.revoked {
		return records.OfficialVisualization{}, errors.New("synthetic revoked")
	}
	return s.graph, nil
}
func (s *fixtureSource) OfficialVisualizationBinding(ctx context.Context, q records.OfficialVisualizationQuery) (records.CogneeBinding, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.revoked {
		return records.CogneeBinding{}, errors.New("synthetic revoked")
	}
	return s.b, nil
}

func main() {
	assets := flag.String("assets", "", "absolute existing graph resource directory")
	out := flag.String("out", "", "fresh private evidence directory")
	synthetic := flag.Bool("synthetic-only", false, "required: never connect to a real backend")
	flag.Parse()
	if !*synthetic || !filepath.IsAbs(*out) {
		panic("explicit synthetic-only/private output required")
	}
	if err := os.Mkdir(*out, 0700); err != nil {
		panic(err)
	}
	bundle, err := officialgraph.LoadBundle(*assets)
	if err != nil {
		panic(err)
	}
	registry := officialgraph.Register(bundle)
	q := records.OfficialVisualizationQuery{Project: uuid.NewString(), Instance: uuid.NewString(), Dataset: uuid.NewString(), MaxNodes: 1000}
	source := &fixtureSource{b: records.CogneeBinding{Operation: uuid.NewString(), Project: q.Project, Instance: q.Instance, Dataset: q.Dataset, Generation: 1}, graph: records.OfficialVisualization{Instance: q.Instance, Dataset: q.Dataset, Scope: "bounded_subgraph", MaxNodes: 1000, Payload: json.RawMessage(`{"nodes":[{"id":"a","name":"PaymentService.charge","type":"CodeSymbol","stage":"other","source_node_set":"synthetic-repo"},{"id":"b","name":"validateAmount","type":"CodeSymbol","stage":"other","source_node_set":"synthetic-repo"}],"links":[{"source":"a","target":"b","relation":"calls","edge_class":"semantic"}],"color_maps":{"node_set":{"synthetic-repo":"#43D9E8"}}}`)}}
	var host *officialgraph.Host
	var selected []officialgraph.Selection
	var mu sync.Mutex
	results := make(chan error, 1)
	mygo.App.WhenReady(func() {
		window := mygo.NewWindow(mygo.WindowOptions{Title: "Memlane 官方图谱开发验证", Width: 1100, Height: 760, Content: ui.View(func(c *ui.Context) {
			ui.Column(c).Fill().Children(func() {
				ui.Text(c, "官方图谱 · 合成数据开发验证").FontSize(13)
				if host != nil {
					host.View(c)
				}
			})
		})})
		host, err = registry.Attach(window, source, func(ctx context.Context, node officialgraph.SelectedNode) error {
			mu.Lock()
			selected = append(selected, node.Selection)
			mu.Unlock()
			return nil
		})
		if err != nil {
			results <- err
			mygo.App.Quit()
			return
		}
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
			defer cancel()
			report := map[string]any{"scope": "actual MyGo/WKWebView and trusted binding with synthetic source; no real official SDK, daily backend, model or code scan"}
			err := func() error {
				if err := host.Load(ctx, q); err != nil {
					return err
				}
				page := host.WebView().Page()
				wait := func(code string) error {
					for ctx.Err() == nil {
						value, err := page.EvalContext(ctx, code)
						if err == nil && value == true {
							return nil
						}
						time.Sleep(100 * time.Millisecond)
					}
					return errors.New("native page readiness unconfirmed")
				}
				if err := wait("Boolean(document.querySelector('canvas')?.width)"); err != nil {
					return err
				}
				time.Sleep(500 * time.Millisecond)
				png, err := host.WebView().CapturePage()
				if err != nil {
					return err
				}
				if err := os.WriteFile(filepath.Join(*out, "webview.png"), png, 0600); err != nil {
					return err
				}
				capture := make(chan error, 1)
				window.Update(func() {
					capture <- captureSystemWindow(window.NativeHandle(), filepath.Join(*out, "system-window.png"))
				})
				select {
				case err := <-capture:
					if err != nil {
						return err
					}
				case <-ctx.Done():
					return ctx.Err()
				}
				report["own_system_window_capture"] = true
				selection := officialgraph.Selection{Epoch: 2, Project: q.Project, Instance: q.Instance, Dataset: q.Dataset, Node: "a"}
				raw, _ := json.Marshal(selection)
				value, err := page.EvalContext(ctx, "(async()=>{await mygo.call('OfficialGraph.Select',"+string(raw)+");return true})()")
				if err != nil || value != true {
					return fmt.Errorf("native selection callback unconfirmed: %v", err)
				}
				selection.Node = "/private/not-a-node"
				raw, _ = json.Marshal(selection)
				value, err = page.EvalContext(ctx, "(async()=>{try{await mygo.call('OfficialGraph.Select',"+string(raw)+");return false}catch{return true}})()")
				if err != nil || value != true {
					return errors.New("unknown node was not rejected")
				}
				mu.Lock()
				count := len(selected)
				mu.Unlock()
				if count != 1 {
					return errors.New("native callback count invalid")
				}
				report["trusted_native_callback"] = true
				report["unknown_node_denied"] = true
				source.mu.Lock()
				source.revoked = true
				source.mu.Unlock()
				for host.Visible() && ctx.Err() == nil {
					time.Sleep(100 * time.Millisecond)
				}
				if host.Visible() {
					return errors.New("native revocation did not hide view")
				}
				if err := wait("document.querySelectorAll('canvas').length===0"); err != nil {
					return err
				}
				report["revocation_hidden_and_page_cleared"] = true
				return nil
			}()
			report["passed"] = err == nil
			if err != nil {
				report["error"] = err.Error()
			}
			raw, _ := json.MarshalIndent(report, "", "  ")
			_ = os.WriteFile(filepath.Join(*out, "native.json"), raw, 0600)
			results <- err
			host.Close()
			mygo.App.Quit()
		}()
	})
	if err := mygo.App.Run(); err != nil {
		panic(err)
	}
	select {
	case err = <-results:
	default:
		err = errors.New("development window closed before verification finished")
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
