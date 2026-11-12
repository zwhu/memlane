package officialgraph

import (
	"context"
	"errors"
	"mime"
	"net/http"
	"path"
	"sync"

	"github.com/egoist/mygo"
)

const pageURL = "memlanegraph://graph/index.html"

type Registry struct {
	mu     sync.RWMutex
	hosts  map[*mygo.Page]*Host
	bundle Bundle
}

// Register is called once before App.Run. Only this small facade is bound;
// lifecycle, source and asset APIs are never callable by a web page.
func Register(bundle Bundle) *Registry {
	r := &Registry{hosts: map[*mygo.Page]*Host{}, bundle: bundle}
	mygo.BindAs("OfficialGraph", &pageService{registry: r})
	mygo.Protocol.Handle("memlanegraph", http.HandlerFunc(r.serve))
	return r
}

func (r *Registry) serve(w http.ResponseWriter, req *http.Request) {
	r.mu.RLock()
	host := r.hosts[mygo.CallerPage(req.Context())]
	r.mu.RUnlock()
	if host == nil || req.Method != "GET" || req.URL.Host != "graph" || req.URL.RawQuery != "" {
		http.NotFound(w, req)
		return
	}
	name := req.URL.Path
	if name == "/" {
		name = "/index.html"
	}
	if name != "/"+path.Base(name) {
		http.NotFound(w, req)
		return
	}
	bytes, ok := r.bundle.files[path.Base(name)]
	if !ok {
		http.NotFound(w, req)
		return
	}
	kind := mime.TypeByExtension(path.Ext(name))
	if name == "/app.js" {
		kind = "text/javascript; charset=utf-8"
	}
	w.Header().Set("Content-Type", kind)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(bytes)
}

func (r *Registry) Attach(window *mygo.Window, source Source, onSelect func(context.Context, SelectedNode) error) (*Host, error) {
	if source == nil {
		return nil, errors.New("graph_source_required")
	}
	view, err := window.NewWebView(mygo.WebViewOptions{URL: "about:blank", BackgroundColor: "#0E1526"})
	if err != nil {
		return nil, err
	}
	h := &Host{view: view, registry: r, source: source, eval: view.Page().EvalContext, onSelect: onSelect, onChange: window.Invalidate}
	r.mu.Lock()
	r.hosts[view.Page()] = h
	r.mu.Unlock()
	window.OnClosed(h.Close)
	view.Page().OnWillNavigate(func(e *mygo.NavigateEvent) {
		if e.URL != pageURL {
			e.PreventDefault()
		}
	})
	if err := view.Page().LoadURL(pageURL); err != nil {
		h.Close()
		return nil, err
	}
	return h, nil
}

type pageService struct{ registry *Registry }

func (s *pageService) host(ctx context.Context) (*Host, error) {
	page := mygo.CallerPage(ctx)
	s.registry.mu.RLock()
	host := s.registry.hosts[page]
	s.registry.mu.RUnlock()
	if page == nil || host == nil || page.URL() != pageURL {
		return nil, errors.New("graph_caller_page_denied")
	}
	return host, nil
}

func (s *pageService) Ready(ctx context.Context) error {
	h, err := s.host(ctx)
	if err != nil {
		return err
	}
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return errors.New("graph_closed")
	}
	h.ready = true
	h.mu.Unlock()
	return h.flush(ctx)
}

func (s *pageService) Select(ctx context.Context, selection Selection) error {
	h, err := s.host(ctx)
	if err != nil {
		return err
	}
	return h.selectNode(ctx, selection)
}
