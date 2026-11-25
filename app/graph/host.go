package graph

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	"github.com/egoist/mygo"
	"github.com/zwhu/memlane/app/api"
	"github.com/zwhu/memlane/app/shell"
)

// Host passes only a bounded official payload and selected identities to
// the pinned renderer. The page never receives credentials or a network API.
type Host struct {
	assets           map[string][]byte
	view             *mygo.WebView
	page             *mygo.Page
	pageReady        chan struct{}
	epoch            uint64
	frame            map[string]any
	ready            bool
	nodes            map[string]json.RawMessage
	project, dataset string
	selected         func(json.RawMessage)
	update           func(func())
	eval             func(context.Context, string) (any, error)
	onRenderError    func(error)
	pending          *graphRender
	rendering        bool
	renderCancel     context.CancelFunc
	closed           bool
	testTransport    bool
}

type SelectRequest struct {
	Epoch   uint64 `json:"epoch"`
	Project string `json:"project"`
	Dataset string `json:"dataset"`
	Node    string `json:"node"`
}

type officialGraphBridge struct {
	host atomic.Pointer[Host]
}

func (b *officialGraphBridge) Ready(ctx context.Context) error {
	if h := b.host.Load(); h != nil && h.pagePublished() {
		return h.Ready(ctx)
	}
	return fmt.Errorf("graph host unavailable")
}

func (b *officialGraphBridge) Select(ctx context.Context, s SelectRequest) error {
	if h := b.host.Load(); h != nil && h.pagePublished() {
		return h.Select(ctx, s)
	}
	return fmt.Errorf("graph host unavailable")
}

var (
	officialGraphBridgeOnce sync.Once
	officialGraphDispatcher officialGraphBridge
)

func ValidateAssets(readFile func(string) ([]byte, error)) (map[string][]byte, error) {
	raw, err := readFile("assets.json")
	if err != nil {
		return nil, err
	}
	var manifest struct {
		Protocol int               `json:"protocol"`
		Upstream string            `json:"upstream"`
		Files    map[string]string `json:"files"`
	}
	if json.Unmarshal(raw, &manifest) != nil || manifest.Protocol != 1 || manifest.Upstream != "8853378eae5366addb064e758e560a456146ae1a" {
		return nil, fmt.Errorf("官方图组件版本不匹配")
	}
	allowed := map[string]bool{"index.html": true, "app.js": true, "app.css": true, "host.css": true, "app.js.LEGAL.txt": true, "COGNEE-LICENSE": true, "THIRD-PARTY-LICENSES.txt": true, "build.json": true}
	assets := make(map[string][]byte, len(manifest.Files))
	size := 0
	for name, digest := range manifest.Files {
		if !allowed[name] {
			return nil, fmt.Errorf("图组件资产不在白名单")
		}
		b, e := readFile(name)
		if e != nil {
			return nil, e
		}
		size += len(b)
		sum := sha256.Sum256(b)
		if size > 16<<20 || hex.EncodeToString(sum[:]) != digest {
			return nil, fmt.Errorf("图组件校验失败")
		}
		assets[name] = b
	}
	for _, name := range []string{"index.html", "app.js", "app.css", "host.css"} {
		if assets[name] == nil {
			return nil, fmt.Errorf("图组件资源缺失")
		}
	}
	return assets, nil
}

func EnsureProtocolAndBridge() {
	officialGraphBridgeOnce.Do(func() {
		mygo.BindAs("OfficialGraph", &officialGraphDispatcher)
		_ = mygo.Protocol.HandleFunc("memlanegraph", officialGraphDispatcher.serveProtocol)
	})
}

func (b *officialGraphBridge) serveProtocol(w http.ResponseWriter, r *http.Request) {
	if g := b.host.Load(); g != nil {
		g.serveProtocol(w, r)
		return
	}
	http.NotFound(w, r)
}

func (g *Host) pagePublished() bool {
	// Closing pageReady publishes the page and binding transport to workers.
	select {
	case <-g.pageReady:
		return true
	default:
		return false
	}
}

func (g *Host) serveProtocol(w http.ResponseWriter, r *http.Request) {
	if !g.pagePublished() {
		http.NotFound(w, r)
		return
	}
	name := r.URL.Path
	if name == "/" {
		name = "/index.html"
	}
	if g.page == nil || mygo.CallerPage(r.Context()) != g.page || r.Method != "GET" || r.URL.Host != "graph" || name != "/"+filepath.Base(name) || r.URL.RawQuery != "" {
		http.NotFound(w, r)
		return
	}
	b, ok := g.assets[filepath.Base(name)]
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", mime.TypeByExtension(filepath.Ext(name)))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(b)
}

func RegisterFromAssets(source map[string][]byte) *Host {
	EnsureProtocolAndBridge()
	assets := make(map[string][]byte, len(source))
	for k, v := range source {
		assets[k] = bytes.Clone(v)
	}
	g := &Host{assets: assets, pageReady: make(chan struct{})}
	officialGraphDispatcher.host.Store(g)
	return g
}

func ValidateDirAssets(directory string) (map[string][]byte, error) {
	if !filepath.IsAbs(directory) {
		return nil, fmt.Errorf("官方图组件目录必须为绝对路径")
	}
	return ValidateAssets(func(name string) ([]byte, error) {
		return os.ReadFile(filepath.Join(directory, name))
	})
}

func Register(directory string) (*Host, error) {
	assets, err := ValidateDirAssets(directory)
	if err != nil {
		return nil, err
	}
	return RegisterFromAssets(assets), nil
}

// RegisterFS verifies and registers official graph assets from an fs.FS.
func RegisterFS(assetsFS fs.FS) (*Host, error) {
	assets, err := ValidateFSAssets(assetsFS)
	if err != nil {
		return nil, err
	}
	return RegisterFromAssets(assets), nil
}

func ValidateFSAssets(assetsFS fs.FS) (map[string][]byte, error) {
	if assetsFS == nil {
		return nil, fmt.Errorf("图组件文件系统不可用")
	}
	return ValidateAssets(func(name string) ([]byte, error) {
		return fs.ReadFile(assetsFS, name)
	})
}

func (g *Host) Attach(win *mygo.Window, selected func(json.RawMessage)) error {
	v, e := win.NewWebView(mygo.WebViewOptions{URL: "about:blank", BackgroundColor: "#0E1526"})
	if e != nil {
		return e
	}
	g.view = v
	g.selected = selected
	g.update = win.Update
	g.eval = v.Page().EvalContext
	g.page = v.Page()
	close(g.pageReady)

	v.Page().OnWillNavigate(func(e *mygo.NavigateEvent) {
		if e.URL != "memlanegraph://graph/index.html" {
			e.PreventDefault()
		}
	})
	return v.Page().LoadURL("memlanegraph://graph/index.html")
}

func (g *Host) Close() {
	officialGraphDispatcher.host.CompareAndSwap(g, nil)
	g.closed = true
	g.pending = nil
	if g.renderCancel != nil {
		g.renderCancel()
	}
	if g.view != nil {
		g.view.Destroy()
	}
}

// Ready and Select are invoked on SDK binding workers. Only their immutable
// transport references are read there; all graph state is accessed on the UI.
func (g *Host) Ready(ctx context.Context) error {
	return g.OnUI(ctx, func() error {
		if g.closed || !g.testTransport && (g.view == nil || mygo.CallerPage(ctx) != g.view.Page()) {
			return fmt.Errorf("graph page unavailable")
		}
		g.ready = true
		return g.Flush(ctx)
	})
}
func (g *Host) Select(ctx context.Context, s SelectRequest) error {
	return g.OnUI(ctx, func() error {
		if g.closed || !g.testTransport && (g.view == nil || mygo.CallerPage(ctx) != g.view.Page()) || s.Epoch != g.epoch || s.Project != g.project || s.Dataset != g.dataset || g.nodes[s.Node] == nil {
			return fmt.Errorf("selected graph identity changed")
		}
		if g.selected != nil {
			g.selected(append(json.RawMessage(nil), g.nodes[s.Node]...))
		}
		return nil
	})
}
func (g *Host) Clear() {
	if g == nil {
		return
	}
	g.epoch++
	g.frame, g.nodes = nil, nil
	_ = g.Flush(context.Background())
}
func (g *Host) ApplyRaw(ctx context.Context, p shell.ProjectRef, dataset string, raw json.RawMessage, ticket *uint64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	data, err := DecodePayload(raw)
	if err != nil {
		return err
	}
	return g.ApplyParsed(p, dataset, data, ticket)
}

// ApplyParsed is UI-owned. The decoder transfers an immutable result to it.
func (g *Host) ApplyParsed(p shell.ProjectRef, dataset string, data Payload, ticket *uint64) error {
	if g.closed || data.raw == nil {
		return fmt.Errorf("graph page or payload unavailable")
	}
	raw, nodes := data.raw, data.nodes
	if ticket != nil && *ticket != g.epoch {
		return fmt.Errorf("graph result superseded")
	}
	if g.frame != nil && g.project == p.ID && g.dataset == dataset {
		if previous, ok := g.frame["payload"].(json.RawMessage); ok && bytes.Equal(previous, raw) {
			return nil // Same official data: preserve the canvas, camera and selection.
		}
	}
	g.epoch++
	g.project = p.ID
	g.dataset = dataset
	g.nodes = nodes
	g.frame = map[string]any{"epoch": g.epoch, "project": p.ID, "instance": "official", "dataset": dataset, "payload": raw}
	return g.Flush(context.Background())
}

func (g *Host) View() *mygo.WebView             { return g.view }
func (g *Host) HasFrame() bool                  { return g.frame != nil }
func (g *Host) Dataset() string                 { return g.dataset }
func (g *Host) Project() string                 { return g.project }
func (g *Host) Epoch() uint64                   { return g.epoch }
func (g *Host) Node(id string) json.RawMessage  { return append(json.RawMessage(nil), g.nodes[id]...) }
func (g *Host) Asset(name string) []byte        { return bytes.Clone(g.assets[name]) }
func (g *Host) SetOnRenderError(fn func(error)) { g.onRenderError = fn }
func (g *Host) IsClosed() bool                  { return g.closed }
func (g *Host) IsRendering() bool               { return g.rendering }
func (g *Host) HasPending() bool                { return g.pending != nil }

func (g *Host) ApplyGraphResponse(ctx context.Context, project shell.ProjectRef, dataset string, resp api.GraphResponse, ticket *uint64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if resp.DatasetID != "" && resp.DatasetID != dataset {
		return fmt.Errorf("官方图数据集身份不匹配")
	}
	data, err := NormalizeGraphResponse(resp)
	if err != nil {
		return err
	}
	return g.ApplyParsed(project, dataset, data, ticket)
}

func NewTestHost(update func(func()), eval func(context.Context, string) (any, error)) *Host {
	return &Host{update: update, eval: eval, ready: eval != nil, testTransport: true}
}

func (g *Host) SetOnSelect(fn func(json.RawMessage)) { g.selected = fn }
