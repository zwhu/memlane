package simpledesktop

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane"
)

// GraphHost passes only a bounded official payload and selected identities to
// the pinned renderer. The page never receives credentials or a network API.
type GraphHost struct {
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
}

type graphSelectRequest struct {
	Epoch   uint64 `json:"epoch"`
	Project string `json:"project"`
	Dataset string `json:"dataset"`
	Node    string `json:"node"`
}

type officialGraphBridge struct {
	host atomic.Pointer[GraphHost]
}

func (b *officialGraphBridge) Ready(ctx context.Context) error {
	if h := b.host.Load(); h != nil {
		return h.Ready(ctx)
	}
	return fmt.Errorf("graph host unavailable")
}

func (b *officialGraphBridge) Select(ctx context.Context, s graphSelectRequest) error {
	if h := b.host.Load(); h != nil {
		return h.Select(ctx, s)
	}
	return fmt.Errorf("graph host unavailable")
}

var (
	officialGraphBridgeOnce sync.Once
	officialGraphDispatcher officialGraphBridge

	embeddedGraphOnce   sync.Once
	embeddedGraphAssets map[string][]byte
	embeddedGraphErr    error
)

func validateGraphAssets(readFile func(string) ([]byte, error)) (map[string][]byte, error) {
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

func registerGraphHostFromAssets(source map[string][]byte) *GraphHost {
	assets := make(map[string][]byte, len(source))
	for k, v := range source {
		assets[k] = v
	}
	g := &GraphHost{assets: assets, pageReady: make(chan struct{})}
	officialGraphDispatcher.host.Store(g)
	officialGraphBridgeOnce.Do(func() {
		mygo.BindAs("OfficialGraph", &officialGraphDispatcher)
	})
	_ = mygo.Protocol.Handle("memlanegraph", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The page reference is published once before loading, then immutable.
		// A closed channel publishes it to protocol workers without a state mutex.
		select {
		case <-g.pageReady:
		default:
			http.NotFound(w, r)
			return
		}
		page := g.page
		name := r.URL.Path
		if name == "/" {
			name = "/index.html"
		}
		if page == nil || mygo.CallerPage(r.Context()) != page || r.Method != "GET" || r.URL.Host != "graph" || name != "/"+filepath.Base(name) || r.URL.RawQuery != "" {
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
	}))
	return g
}

func RegisterGraph(directory string) (*GraphHost, error) {
	if !filepath.IsAbs(directory) {
		return nil, fmt.Errorf("官方图组件目录必须为绝对路径")
	}
	assets, err := validateGraphAssets(func(name string) ([]byte, error) {
		return os.ReadFile(filepath.Join(directory, name))
	})
	if err != nil {
		return nil, err
	}
	return registerGraphHostFromAssets(assets), nil
}

// RegisterGraphFS verifies and registers official graph assets from an fs.FS.
func RegisterGraphFS(assetsFS fs.FS) (*GraphHost, error) {
	if assetsFS == nil {
		return nil, fmt.Errorf("图组件文件系统不可用")
	}
	assets, err := validateGraphAssets(func(name string) ([]byte, error) {
		return fs.ReadFile(assetsFS, name)
	})
	if err != nil {
		return nil, err
	}
	return registerGraphHostFromAssets(assets), nil
}

func loadEmbeddedGraphHost() (*GraphHost, error) {
	embeddedGraphOnce.Do(func() {
		sub, err := fs.Sub(memlane.RuntimeAssets, "web/official-graph/dist")
		if err != nil {
			embeddedGraphErr = err
			return
		}
		embeddedGraphAssets, embeddedGraphErr = validateGraphAssets(func(name string) ([]byte, error) {
			return fs.ReadFile(sub, name)
		})
	})
	if embeddedGraphErr != nil {
		return nil, embeddedGraphErr
	}
	return registerGraphHostFromAssets(embeddedGraphAssets), nil
}
func (g *GraphHost) attach(win *mygo.Window, selected func(json.RawMessage)) error {
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
	win.OnClosed(func() {
		g.closed = true
		g.pending = nil
		if g.renderCancel != nil {
			g.renderCancel()
		}
		v.Destroy()
	})
	return v.Page().LoadURL("memlanegraph://graph/index.html")
}

// Ready and Select are invoked on SDK binding workers. Only their immutable
// transport references are read there; all graph state is accessed on the UI.
func (g *GraphHost) Ready(ctx context.Context) error {
	return g.onUI(ctx, func() error {
		if g.closed || g.view == nil || mygo.CallerPage(ctx) != g.view.Page() {
			return fmt.Errorf("graph page unavailable")
		}
		g.ready = true
		return g.flush(ctx)
	})
}
func (g *GraphHost) Select(ctx context.Context, s graphSelectRequest) error {
	return g.onUI(ctx, func() error {
		if g.closed || g.view == nil || mygo.CallerPage(ctx) != g.view.Page() || s.Epoch != g.epoch || s.Project != g.project || s.Dataset != g.dataset || g.nodes[s.Node] == nil {
			return fmt.Errorf("selected graph identity changed")
		}
		if g.selected != nil {
			g.selected(append(json.RawMessage(nil), g.nodes[s.Node]...))
		}
		return nil
	})
}
func (g *GraphHost) clear() {
	if g == nil {
		return
	}
	g.epoch++
	g.frame, g.nodes = nil, nil
	_ = g.flush(context.Background())
}
func (g *GraphHost) apply(ctx context.Context, p Project, dataset string, raw json.RawMessage) error {
	return g.applyCurrent(ctx, p, dataset, raw, nil)
}
func (g *GraphHost) applyCurrent(ctx context.Context, p Project, dataset string, raw json.RawMessage, ticket *uint64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	data, err := decodeGraph(raw)
	if err != nil {
		return err
	}
	return g.applyParsed(p, dataset, data, ticket)
}

type graphData struct {
	raw   json.RawMessage
	nodes map[string]json.RawMessage
}

func decodeGraph(raw json.RawMessage) (graphData, error) {
	var payload struct {
		Nodes []json.RawMessage `json:"nodes"`
		Links []struct {
			Source string `json:"source"`
			Target string `json:"target"`
		} `json:"links"`
		Colors map[string]any `json:"color_maps"`
	}
	if json.Unmarshal(raw, &payload) != nil || payload.Nodes == nil || payload.Links == nil || payload.Colors == nil || len(payload.Nodes) > 1000 || len(payload.Links) > 50000 {
		return graphData{}, fmt.Errorf("官方图数据结构不匹配")
	}
	nodes := map[string]json.RawMessage{}
	for _, node := range payload.Nodes {
		var n struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(node, &n) != nil || n.ID == "" || nodes[n.ID] != nil {
			return graphData{}, fmt.Errorf("官方图节点身份不匹配")
		}
		nodes[n.ID] = node
	}
	for _, link := range payload.Links {
		if nodes[link.Source] == nil || nodes[link.Target] == nil {
			return graphData{}, fmt.Errorf("官方图边身份不匹配")
		}
	}
	return graphData{raw: raw, nodes: nodes}, nil
}

// applyParsed is UI-owned. The decoder transfers an immutable result to it.
func (g *GraphHost) applyParsed(p Project, dataset string, data graphData, ticket *uint64) error {
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
	return g.flush(context.Background())
}
func (a *App) AttachGraph(win *mygo.Window) error {
	if a.graph == nil && a.graphError == "" {
		host, err := loadEmbeddedGraphHost()
		a.graph = host
		if err != nil {
			a.graphError = err.Error()
		}
	}
	if a.graph == nil {
		return nil
	}
	a.graph.onRenderError = func(err error) {
		if a.page == "图谱" {
			a.graphError = err.Error()
		}
	}
	return a.graph.attach(win, func(raw json.RawMessage) { a.graphSelection = string(raw) })
}
func (a *App) loadGraph() {
	p, ok := a.current()
	if !ok {
		a.graphError = "请先关联项目"
		return
	}
	if a.graphMode != "代码结构" {
		a.loadGraphPending(p)
	}
	dataset := p.Dataset
	if a.graphMode == "代码结构" {
		dataset = p.CodeDataset
	}
	if dataset == "" {
		a.graphError = "尚未配置代码数据组"
		return
	}
	a.graphError = ""
	a.graphLoading = true
	var ticket uint64
	if a.graph != nil {
		ticket = a.graph.epoch
	}
	epoch := a.epoch
	client := a.Client
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		raw, e := client.request(ctx, "GET", "/api/v1/visualize/json?dataset_id="+url.QueryEscape(dataset)+"&max_nodes=1000&include_session_events=false", nil)
		var data graphData
		if e == nil {
			data, e = decodeGraph(raw)
		}
		a.Update(func() {
			if epoch != a.epoch {
				return
			}
			a.graphLoading = false
			if e != nil {
				a.graphError = e.Error()
				return
			}
			if a.graph == nil {
				a.graphError = "官方图组件不可用"
				return
			}
			if err := a.graph.applyParsed(p, dataset, data, &ticket); err != nil {
				a.graphError = err.Error()
				return
			}
			if a.graphSelection != "" {
				var selected struct {
					ID string `json:"id"`
				}
				_ = json.Unmarshal([]byte(a.graphSelection), &selected)
				a.graphSelection = string(a.graph.nodes[selected.ID])
			}
		})
	}()
}
func (a *App) graphView(c *ui.Context) {
	ui.Column(c).Grow(1).MinHeight(0).Children(func() {
		a.graphToolbar(c)
		if p, ok := a.current(); ok && a.graphMode == "代码结构" && !p.CodeEnabled {
			a.codeGraphEmpty(c)
			return
		}
		if a.graphError != "" {
			ui.Text(c, a.graphError).FontSize(11).TextColor(muted).Margin(10, 32)
		}
		if a.graphMode == "代码结构" && a.codeStatus != "" {
			ui.Text(c, a.codeStatus).FontSize(11).TextColor(muted).Margin(10, 32)
		}
		if a.graph != nil && a.graph.view != nil {
			ui.Column(c.Key("graph-canvas-container")).Grow(1).MinHeight(0).Margin(0, pageInset, pageInset, pageInset).Radius(12).Clip().Children(func() {
				ui.WebView(c.Key("official-graph-canvas"), a.graph.view).Grow(1).MinHeight(0)
			})
		}
		if a.graphSelection != "" {
			ui.Text(c, "选中节点 · "+a.graphSelection).FontSize(10).TextColor(muted).MaxLines(3).Margin(10, 32)
			button(c, "查看官方文档出处", a.graphSource)
		}
	})
}
func (a *App) toggleCode(enabled bool) {
	a.graphError = ""
	p, ok := a.current()
	if !ok || p.Path == "" {
		a.graphError = "代码索引需要当前项目的仓库路径"
		return
	}
	if enabled && a.codeIndexBlocked() {
		return
	}
	if a.Config.ProjectControl != nil {
		a.toggleManagedCode(p, enabled)
		return
	}
	if enabled && p.CodeDataset == "" {
		a.codeBusy = true
		a.codeStatus = "正在准备本项目的官方代码数据组…"
		client := a.Client
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			dataset, err := client.ensureCodeDataset(ctx, p)
			a.Update(func() {
				a.codeBusy = false
				if err != nil {
					a.codeStatus = "代码数据组准备失败：" + err.Error()
					return
				}
				index := -1
				for idx, current := range a.Config.Projects {
					if current.ID == p.ID && current.Path == p.Path && current.Dataset == p.Dataset {
						index = idx
						break
					}
				}
				if index < 0 {
					a.codeStatus = "项目关联已改变；已保留官方数据组，未开始索引"
					return
				}
				before := a.Config.Projects[index]
				p.CodeDataset = dataset
				p.CodeEnabled = true
				a.Config.Projects[index] = p
				if err := a.saveProjects(); err != nil {
					a.Config.Projects[index] = before
					a.codeStatus = "代码关联保存失败；未开始索引：" + err.Error()
					return
				}
				a.indexProjectCode(p)
			})
		}()
		return
	}
	a.Config.Projects[a.project].CodeEnabled = enabled
	if err := a.saveProjects(); err != nil {
		a.Config.Projects[a.project].CodeEnabled = !enabled
		a.graphError = err.Error()
		return
	}
	if enabled {
		a.indexCode()
	} else {
		a.codeStatus = "已停止发起新扫描；已有官方任务未请求取消"
	}
}
func (a *App) saveProjects() error {
	b, e := os.ReadFile(a.Config.ConfigFile)
	if e != nil {
		return e
	}
	var fields map[string]json.RawMessage
	if e = json.Unmarshal(b, &fields); e != nil {
		return e
	}
	fields["projects"], e = json.Marshal(a.Config.Projects)
	if e != nil {
		return e
	}
	b, e = json.MarshalIndent(fields, "", "  ")
	if e != nil {
		return e
	}
	tmp := a.Config.ConfigFile + ".tmp"
	if e = os.WriteFile(tmp, b, 0600); e != nil {
		return e
	}
	return os.Rename(tmp, a.Config.ConfigFile)
}
func (a *App) indexCode() {
	p, ok := a.current()
	if ok {
		a.indexProjectCode(p)
	}
}
func (a *App) indexProjectCode(p Project) {
	if !p.CodeEnabled || p.CodeDataset == "" || a.codeIndexBlocked() {
		return
	}
	a.codeBusy = true
	a.codeStatus = "官方正在索引当前仓库；等待实际结果…"
	client := a.Client
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		result, err := client.indexProject(ctx, p)
		a.Update(func() {
			a.codeBusy = false
			if errors.Is(err, errOfficialCodeActive) {
				a.codeRemoteActive = true
				a.codeStatus = "官方已有代码索引运行中；未重复提交"
				return
			}
			if err != nil {
				a.codeUnknown = true
				a.codeStatus = "索引结果未确认：" + err.Error() + "；未重发请求"
				return
			}
			a.codeStatus = fmt.Sprintf("官方代码索引：%s · 已处理 %d 个仓库 · %s", result.Status, result.Items, result.Run)
			if result.Status == "completed" {
				if current, ok := a.current(); ok && current.ID == p.ID && a.page == "图谱" {
					a.graphMode = "代码结构"
					a.graph.clear()
					a.loadGraph()
				}
			}
		})
	}()
}

type codeIndexResult struct {
	Status  string `json:"status"`
	Dataset string `json:"dataset_id"`
	Run     string `json:"pipeline_run_id"`
	Items   int    `json:"items_processed"`
}

func (c Client) ensureCodeDataset(ctx context.Context, p Project) (string, error) {
	name := p.ID + "-code"
	b, err := c.request(ctx, "POST", "/api/v1/datasets", map[string]string{"name": name})
	if err != nil {
		return "", err
	}
	var dataset struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if json.Unmarshal(b, &dataset) != nil || dataset.ID == "" || dataset.Name != name || dataset.ID == p.Dataset {
		return "", fmt.Errorf("官方代码数据组身份不匹配")
	}
	return dataset.ID, nil
}
func (c Client) indexProject(ctx context.Context, p Project) (codeIndexResult, error) {
	state, err := c.codeActivity(ctx, p)
	if err != nil {
		return codeIndexResult{}, err
	}
	if state.Active {
		return codeIndexResult{}, errOfficialCodeActive
	}
	c.HTTP = &http.Client{Timeout: 30 * time.Minute, Transport: sharedHTTPTransport, CheckRedirect: noRedirectFunc}
	b, err := c.request(ctx, "POST", "/api/v1/remember", url.Values{"raw_data": {p.Path}, "datasetId": {p.CodeDataset}, "content_type": {"code"}, "run_in_background": {"false"}, "index_vectors": {"false"}, "self_improvement": {"false"}})
	var result codeIndexResult
	if err != nil {
		return result, err
	}
	if json.Unmarshal(b, &result) != nil || result.Dataset != p.CodeDataset || result.Status == "" {
		return result, fmt.Errorf("官方索引回执身份或结构不匹配")
	}
	if result.Status != "completed" && result.Status != "errored" {
		return result, fmt.Errorf("官方索引终态未知：%s", result.Status)
	}
	return result, nil
}

func (a *App) graphSource() {
	p, ok := a.current()
	if !ok || a.graphSelection == "" {
		return
	}
	var node map[string]any
	if json.Unmarshal([]byte(a.graphSelection), &node) != nil {
		return
	}
	id, _ := node["document_id"].(string)
	if id == "" {
		id, _ = node["data_id"].(string)
	}
	if id == "" {
		id, _ = node["id"].(string)
	}
	dataset := p.Dataset
	if a.graphMode == "代码结构" {
		dataset = p.CodeDataset
	}
	epoch := a.epoch
	client := a.Client
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		i, err := client.documentByID(ctx, dataset, id)
		a.Update(func() {
			if epoch != a.epoch {
				return
			}
			if err != nil {
				a.graphError = "该节点没有可读取的官方文档出处；节点面板保留真实来源字段。"
				return
			}
			a.navigate("知识", a.project)
			a.selectItem(a.state(), i)
		})
	}()
}

var errOfficialCodeActive = errors.New("official code index already active")

type codeActivityState struct {
	Active      bool
	Status, Run string
}

func (c Client) codeActivity(ctx context.Context, p Project) (codeActivityState, error) {
	state := codeActivityState{}
	if control, ok := c.controlClient(); ok && p.ID != "" {
		res, err := control.projectCodeActivityV2(ctx, p.ID)
		if err == nil {
			if p.CodeDataset != "" && res.CodeDatasetID != "" && res.CodeDatasetID != p.CodeDataset {
				return state, fmt.Errorf("官方索引运行归属不匹配")
			}
			return codeActivityState{
				Active: res.Active,
				Status: res.Status,
				Run:    res.RunID,
			}, nil
		}
		if isControlAuthDenied(err) {
			return state, err
		}
		if isControlErrorCode(err, http.StatusConflict, "code_dataset_not_configured") ||
			isControlErrorCode(err, http.StatusNotFound, "code_dataset_not_found") {
			return state, err
		}
		return state, err
	}
	generation, err := c.codeDatasetCreated(ctx, p.CodeDataset)
	if err != nil {
		return state, err
	}
	seen := map[string]bool{}
	for offset := 0; offset <= 100000; offset += 500 {
		var rows []struct {
			ID        string `json:"id"`
			Dataset   string `json:"dataset_id"`
			Kind      string `json:"kind"`
			Pipeline  string `json:"pipeline_name"`
			Operation string `json:"operation_name"`
			Status    string `json:"status"`
			Outcome   string `json:"outcome"`
			Run       string `json:"pipeline_run_id"`
			Ended     string `json:"ended_at"`
			Created   string `json:"created_at"`
			Started   string `json:"started_at"`
		}
		err := c.get(ctx, "/api/v1/activity/pipeline-runs?dataset_id="+url.QueryEscape(p.CodeDataset)+"&limit=500&offset="+strconv.Itoa(offset), &rows)
		if err != nil {
			return state, err
		}
		for _, row := range rows {
			if row.Dataset != p.CodeDataset {
				return state, fmt.Errorf("官方索引运行归属不匹配")
			}
			if row.Pipeline != "code_graph_pipeline" && row.Operation != "remember" {
				continue
			}
			before, err := codeRunPredatesGeneration(row.Created, row.Started, generation)
			if err != nil {
				return state, err
			}
			if before {
				continue
			}
			key := row.Run
			if key == "" {
				key = row.ID
			}
			if seen[key] {
				continue
			}
			seen[key] = true
			if state.Run == "" {
				state.Run = row.Run
				state.Status = row.Status
				if state.Status == "" {
					state.Status = row.Outcome
				}
			}
			if row.Status == "DATASET_PROCESSING_STARTED" || row.Status == "DATASET_PROCESSING_INITIATED" || row.Kind == "operation" && row.Outcome == "" && row.Ended == "" {
				state.Active = true
				state.Run = row.Run
				state.Status = "running"
			}
		}
		if len(rows) < 500 {
			fresh, err := c.codeDatasetCreated(ctx, p.CodeDataset)
			if err != nil {
				return state, err
			}
			if !fresh.Equal(generation) {
				return state, fmt.Errorf("官方代码数据组已重新创建，请刷新后再操作")
			}
			return state, nil
		}
	}
	return state, fmt.Errorf("官方索引历史超出本次有界读取，运行状态未确认")
}
func (a *App) restoreCodeState() {
	projects := append([]Project(nil), a.Config.Projects...)
	client := a.Client
	go func() {
		for _, p := range projects {
			if !p.CodeEnabled || p.CodeDataset == "" {
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			state, err := client.codeActivity(ctx, p)
			cancel()
			a.Update(func() {
				if a.codeBusy {
					return
				}
				if err != nil {
					a.codeUnknown = true
					a.codeStatus = "官方代码索引状态读取失败；未发起新扫描：" + err.Error()
					return
				}
				a.codeRemoteActive = state.Active
				a.codeUnknown = false
				a.codeStatus = "官方代码索引状态：" + state.Status
				if state.Run != "" {
					a.codeStatus += " · " + state.Run
				}
				if state.Status == "" {
					a.codeStatus = "官方尚未提供代码索引运行记录；未自动重发"
				}
			})
		}
	}()
}

// The pinned API serializes dataset creation as createdAt, and activity times
// as ISO-8601 UTC (some activity rows omit the explicit UTC suffix). Neither
// wall-clock age nor local restart markers identify a dataset incarnation.
func officialTimestamp(value string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999999", "2006-01-02 15:04:05.999999999"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, fmt.Errorf("官方索引时间缺失或格式不匹配；运行世代未确认")
}
func (c Client) codeDatasetCreated(ctx context.Context, id string) (time.Time, error) {
	var datasets []struct {
		ID      string `json:"id"`
		Created string `json:"createdAt"`
	}
	if err := c.get(ctx, "/api/v1/datasets", &datasets); err != nil {
		return time.Time{}, err
	}
	var generation time.Time
	found := false
	for _, dataset := range datasets {
		if dataset.ID != id {
			continue
		}
		if found {
			return time.Time{}, fmt.Errorf("官方代码数据组身份重复")
		}
		found = true
		var err error
		generation, err = officialTimestamp(dataset.Created)
		if err != nil {
			return time.Time{}, err
		}
	}
	if !found {
		return time.Time{}, fmt.Errorf("官方代码数据组不存在；未发起索引")
	}
	return generation, nil
}
func codeRunPredatesGeneration(created, started string, generation time.Time) (bool, error) {
	rowCreated, err := officialTimestamp(created)
	if err != nil {
		return false, err
	}
	if started == "" {
		return rowCreated.Before(generation), nil
	}
	runStarted, err := officialTimestamp(started)
	if err != nil {
		return false, err
	}
	// A row written at/after recreation may describe continuing work; retain it
	// even when its declared start is older. Equality belongs to the new dataset.
	return rowCreated.Before(generation) && runStarted.Before(generation), nil
}
