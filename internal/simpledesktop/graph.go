package simpledesktop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane"
	"github.com/zwhu/memlane/app/api"
	appgraph "github.com/zwhu/memlane/app/graph"
	"github.com/zwhu/memlane/app/shell"
)

func (a *App) toggleCode(enabled bool) {
	a.graphController.Error = ""
	p, ok := a.current()
	if !ok || p.Path == "" {
		a.graphController.Error = "代码索引需要当前项目的仓库路径"
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
		a.graphController.CodeBusy = true
		a.graphController.CodeStatus = "正在准备本项目的官方代码数据组…"
		client := a.Client
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			dataset, err := client.ensureCodeDataset(ctx, p)
			a.Update(func() {
				a.graphController.CodeBusy = false
				if err != nil {
					a.graphController.CodeStatus = "代码数据组准备失败：" + err.Error()
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
					a.graphController.CodeStatus = "项目关联已改变；已保留官方数据组，未开始索引"
					return
				}
				before := a.Config.Projects[index]
				p.CodeDataset = dataset
				p.CodeEnabled = true
				a.Config.Projects[index] = p
				if err := a.saveProjects(); err != nil {
					a.Config.Projects[index] = before
					a.graphController.CodeStatus = "代码关联保存失败；未开始索引：" + err.Error()
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
		a.graphController.Error = err.Error()
		return
	}
	if enabled {
		a.indexCode()
	} else {
		a.graphController.CodeStatus = "已停止发起新扫描；已有官方任务未请求取消"
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
	a.graphController.CodeBusy = true
	a.graphController.CodeStatus = "官方正在索引当前仓库；等待实际结果…"
	client := a.Client
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		result, err := client.indexProject(ctx, p)
		a.Update(func() {
			a.graphController.CodeBusy = false
			if errors.Is(err, errOfficialCodeActive) {
				a.graphController.CodeRemoteActive = true
				a.graphController.CodeStatus = "官方已有代码索引运行中；未重复提交"
				return
			}
			if err != nil {
				a.graphController.CodeUnknown = true
				a.graphController.CodeStatus = "索引结果未确认：" + err.Error() + "；未重发请求"
				return
			}
			a.graphController.CodeStatus = fmt.Sprintf("官方代码索引：%s · 已处理 %d 个仓库 · %s", result.Status, result.Items, result.Run)
			if result.Status == "completed" {
				if current, ok := a.current(); ok && current.ID == p.ID && a.page == "图谱" {
					a.graphController.Mode = "代码结构"
					a.graphController.Host.Clear()
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
	if !ok || a.graphController.Selection == "" || a.Update == nil {
		return
	}
	var node map[string]any
	if json.Unmarshal([]byte(a.graphController.Selection), &node) != nil {
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
	if a.graphController.Mode == "代码结构" {
		dataset = p.CodeDataset
	}
	adapter := appShellAdapter{a}
	fence := shell.CaptureFence(adapter)
	client, update := a.Client, a.Update
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		i, err := client.documentByID(ctx, dataset, id)
		update(func() {
			if !fence.Valid(adapter) {
				return
			}
			if err != nil {
				a.graphController.Error = "该节点没有可读取的官方文档出处；节点面板保留真实来源字段。"
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
				if a.graphController.CodeBusy {
					return
				}
				if err != nil {
					a.graphController.CodeUnknown = true
					a.graphController.CodeStatus = "官方代码索引状态读取失败；未发起新扫描：" + err.Error()
					return
				}
				a.graphController.CodeRemoteActive = state.Active
				a.graphController.CodeUnknown = false
				a.graphController.CodeStatus = "官方代码索引状态：" + state.Status
				if state.Run != "" {
					a.graphController.CodeStatus += " · " + state.Run
				}
				if state.Status == "" {
					a.graphController.CodeStatus = "官方尚未提供代码索引运行记录；未自动重发"
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

type GraphHost = appgraph.Host
type graphLoad = appgraph.LoadResult

func RegisterGraph(dir string) (*GraphHost, error)     { return appgraph.Register(dir) }
func RegisterGraphFS(assets fs.FS) (*GraphHost, error) { return appgraph.RegisterFS(assets) }

var embeddedGraph = sync.OnceValues(func() (fs.FS, error) {
	return fs.Sub(memlane.RuntimeAssets, "web/official-graph/dist")
})

func loadEmbeddedGraphHost() (*GraphHost, error) {
	assets, err := embeddedGraph()
	if err != nil {
		return nil, err
	}
	return appgraph.RegisterFS(assets)
}

func (a *App) initGraph(dir string) {
	adapter := appShellAdapter{a}
	assets, _ := embeddedGraph()
	a.graphController.Controller = appgraph.New(appgraph.Deps{
		Router: adapter, Fence: adapter, Theme: adapter, AssetsDir: dir, EmbeddedAssets: assets,
		CurrentProject:     func() (shell.ProjectRef, bool) { p, ok := a.current(); return projectRef(p), ok },
		IsSetupOnly:        a.isSetupOnlyRuntime,
		IsWindowHidden:     func() bool { return a.windowHidden || a.windowInactive },
		HasControl:         func() bool { return a.Config.ProjectControl != nil },
		OnOpenSettings:     a.openSettingsFromDeferredBanner,
		OnPreviewToolGraph: a.previewToolGraph,
		OnToggleCode:       a.toggleCode, OnIndexCode: a.indexCode, OnRestoreCodeState: a.restoreCodeState,
		OnOpenNodeSource:  a.graphSource,
		OnLoadPending:     func(p shell.ProjectRef) { a.loadGraphPending(desktopProject(p)) },
		RenderPendingHint: func(c *ui.Context, p shell.ProjectRef) { a.graphPendingHint(c, desktopProject(p)) },
		OnWindowClosed:    a.startupDeferred.stopAllTimers,
		OnRenderError: func(err error) {
			if a.page == "图谱" {
				a.graphController.Error = err.Error()
			}
		},
	})
}

func (a *App) configureGraphTransport() {
	g := &a.graphController.Controller
	g.ClientFactory = nil
	g.Dispatcher = nil
	if a.Update == nil {
		return
	}
	g.Dispatcher = shell.DispatchFunc(a.Update)
	client := a.Client
	g.RawFetcher = func(ctx context.Context, _ shell.ProjectRef, dataset string) (json.RawMessage, error) {
		return client.request(ctx, "GET", "/api/v1/visualize/json?dataset_id="+url.QueryEscape(dataset)+"&max_nodes=1000&include_session_events=false", nil)
	}
	if control, ok := a.accessClient(); ok {
		g.ClientFactory = func() (*api.Client, error) {
			identity, err := readingIdentity(Client{Config: client.Config})
			if err != nil {
				return nil, err
			}
			return newAppAPIClient(control, identity)
		}
	}
}

func (a *App) BindGraphWindow(win *mygo.Window) { a.graphController.BindWindow(win) }
func (a *App) verifyGraphAssetsAsync() {
	a.configureGraphTransport()
	a.graphController.VerifyAssetsAsync()
}
func (a *App) EnsureGraphAttached() {
	a.configureGraphTransport()
	a.graphController.EnsureAttached()
}
func (a *App) PrewarmGraph() {
	a.configureGraphTransport()
	a.graphController.Prewarm()
}
func (a *App) AttachGraph(win *mygo.Window) error {
	a.configureGraphTransport()
	return a.graphController.Attach(win)
}
func (a *App) loadGraph() {
	a.configureGraphTransport()
	a.graphController.Load()
}
func (a *App) graphView(c *ui.Context)    { a.graphController.RenderView(c) }
func (a *App) resetGraphView(mode string) { a.graphController.ResetView(mode) }
func (a *App) cancelGraphLoad()           { a.graphController.CancelLoad() }
func (a *App) codeIndexBlocked() bool     { return a.graphController.CodeIndexBlocked() }
