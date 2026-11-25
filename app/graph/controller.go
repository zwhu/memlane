package graph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/egoist/mygo"
	"github.com/zwhu/memlane/app/api"
	"github.com/zwhu/memlane/app/shell"
)

const (
	ModeEntity = "实体关系"
	ModeCode   = "代码结构"
)

type RawFetcher func(ctx context.Context, project shell.ProjectRef, dataset string) (json.RawMessage, error)

type Deps struct {
	Dispatcher                              shell.Dispatcher
	Router                                  shell.Router
	Fence                                   shell.SessionFence
	Theme                                   shell.ThemeProvider
	Client                                  *api.Client
	ClientFactory                           func() (*api.Client, error)
	RawFetcher                              RawFetcher
	CurrentProject                          func() (shell.ProjectRef, bool)
	IsSetupOnly, IsWindowHidden, HasControl func() bool
	AssetsDir                               string
	EmbeddedAssets                          fs.FS

	OnOpenSettings                                    func()
	OnPreviewToolGraph                                func(projectID string)
	OnToggleCode                                      func(enabled bool)
	OnIndexCode, OnRestoreCodeState, OnOpenNodeSource func()
	OnLoadPending                                     func(shell.ProjectRef)
	RenderPendingHint                                 PendingHintRenderer
	OnWindowClosed                                    func()
	OnRenderError                                     func(error)
}

type Controller struct {
	Deps
	Mode                                             string
	Host                                             *Host
	Window                                           *mygo.Window
	WindowClosed                                     bool
	Selection, Error, CodeStatus                     string
	Loading, CodeBusy, CodeUnknown, CodeRemoteActive bool

	Assets                         map[string][]byte
	AssetsCheck                    *assetCheck
	AssetsChecked, AttachRequested bool
	AssetsErr, AttachErr           error
	AttachHost                     func(*Host) error
	PendingLoad                    *LoadResult
	LoadRequest                    uint64
	cancelLoad                     context.CancelFunc
}

type assetCheck struct {
	done   chan struct{}
	assets map[string][]byte
	err    error
}

type LoadResult struct {
	fence   shell.FenceSnapshot
	request uint64
	project shell.ProjectRef
	dataset string
	data    Payload
}

func New(deps Deps) Controller {
	EnsureProtocolAndBridge()
	return Controller{Deps: deps, Mode: ModeEntity}
}

func (g *Controller) BindWindow(win *mygo.Window) {
	if win == nil || g.Window != nil {
		return
	}
	g.Window = win
	g.WindowClosed = win.IsDestroyed()
	if g.WindowClosed {
		return
	}
	win.OnClosed(func() {
		g.WindowClosed = true
		g.CancelLoad()
		if g.Host != nil {
			g.Host.Close()
		}
		if g.OnWindowClosed != nil {
			g.OnWindowClosed()
		}
	})
}

func verifyAssets(dir string, embedded fs.FS) (map[string][]byte, error) {
	if dir != "" {
		return ValidateDirAssets(dir)
	}
	return ValidateFSAssets(embedded)
}

func (g *Controller) VerifyAssetsAsync() {
	if g.WindowClosed || g.Host != nil || g.AttachErr != nil || g.AssetsChecked ||
		g.AssetsCheck != nil || g.Dispatcher == nil || (g.Window == nil && g.AttachHost == nil) {
		return
	}
	check := &assetCheck{done: make(chan struct{})}
	g.AssetsCheck = check
	dir, embedded, dispatcher := g.AssetsDir, g.EmbeddedAssets, g.Dispatcher
	go func() {
		check.assets, check.err = verifyAssets(dir, embedded)
		close(check.done)
		dispatcher.Dispatch(func() {
			if g.AssetsCheck != check {
				return
			}
			g.applyAssetCheck(check.assets, check.err)
			if g.AttachRequested {
				_ = g.attachVerified()
			}
		})
	}()
}

func (g *Controller) EnsureAttached() {
	if flag(g.IsSetupOnly) || g.WindowClosed || g.Host != nil || g.AttachErr != nil || (g.Window == nil && g.AttachHost == nil) {
		return
	}
	g.AttachRequested = true
	if g.AssetsChecked {
		_ = g.attachVerified()
		return
	}
	g.VerifyAssetsAsync()
}

func (g *Controller) Prewarm() {
	if !flag(g.IsWindowHidden) {
		g.EnsureAttached()
	}
}

func (g *Controller) Attach(win *mygo.Window) error {
	g.BindWindow(win)
	if g.WindowClosed {
		return nil
	}
	if !g.AssetsChecked {
		if check := g.AssetsCheck; check != nil {
			<-check.done
			g.applyAssetCheck(check.assets, check.err)
		} else {
			g.applyAssetCheck(verifyAssets(g.AssetsDir, g.EmbeddedAssets))
		}
	}
	return g.attachVerified()
}

func (g *Controller) applyAssetCheck(assets map[string][]byte, err error) {
	g.AssetsCheck = nil
	g.Assets, g.AssetsErr, g.AssetsChecked = assets, err, true
	if err != nil {
		g.CancelLoad()
		g.Error = err.Error()
	}
}

func (g *Controller) attachVerified() error {
	if g.AssetsErr != nil {
		return g.AssetsErr
	}
	if g.AttachErr != nil {
		return g.AttachErr
	}
	if g.WindowClosed || g.Host != nil || (g.Window == nil && g.AttachHost == nil) {
		return nil
	}
	h := RegisterFromAssets(g.Assets)
	h.SetOnRenderError(func(err error) {
		if g.OnRenderError != nil {
			g.OnRenderError(err)
			return
		}
		g.Error = err.Error()
	})
	attach := g.AttachHost
	if attach == nil {
		attach = func(h *Host) error {
			return h.Attach(g.Window, func(raw json.RawMessage) { g.Selection = string(raw) })
		}
	}
	if err := attach(h); err != nil {
		h.Close()
		g.CancelLoad()
		g.AttachErr, g.Error = err, err.Error()
		return err
	}
	g.Host = h
	if pending := g.PendingLoad; pending != nil {
		g.PendingLoad, g.Loading = nil, false
		if g.currentLoad(*pending) {
			g.applyLoad(*pending, nil)
		}
	}
	return nil
}

func (g *Controller) ReloadAssets(dir string, graphVisible bool) {
	changed := dir != g.AssetsDir
	g.AssetsDir = dir
	if !changed && g.Host != nil && g.AttachErr == nil {
		return
	}
	closed := changed && g.Host != nil
	if closed {
		g.CancelLoad()
		g.Host.Close()
		g.Host, g.Selection = nil, ""
	}
	if changed || (g.AssetsErr != nil && g.Error == g.AssetsErr.Error()) ||
		(g.AttachErr != nil && g.Error == g.AttachErr.Error()) {
		g.Error = ""
	}
	g.AttachErr, g.AssetsErr, g.Assets, g.AssetsCheck, g.AssetsChecked = nil, nil, nil, nil, false
	g.VerifyAssetsAsync()
	if g.AttachRequested || graphVisible {
		g.EnsureAttached()
	}
	if closed && graphVisible {
		g.Load()
	}
}

func (g *Controller) Load() {
	if flag(g.IsSetupOnly) {
		g.CancelLoad()
		g.Error = ""
		return
	}
	g.EnsureAttached()
	if g.AssetsErr != nil || g.AttachErr != nil || g.WindowClosed || g.Dispatcher == nil {
		return
	}
	project, ok := g.project()
	if !ok {
		g.Error = "请先关联项目"
		return
	}
	dataset := project.DatasetID
	if g.Mode == ModeCode {
		dataset = project.CodeDatasetID
	} else if g.OnLoadPending != nil {
		g.OnLoadPending(project)
	}
	g.CancelLoad()
	if dataset == "" {
		g.Error = "尚未配置代码数据组"
		return
	}
	g.Error, g.Loading = "", true
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	g.cancelLoad = cancel
	result := LoadResult{fence: shell.CaptureFence(g.Fence), request: g.LoadRequest, project: project, dataset: dataset}
	client, makeClient, fetch, dispatch := g.Client, g.ClientFactory, g.RawFetcher, g.Dispatcher
	var ticket *uint64
	if g.Host != nil {
		epoch := g.Host.Epoch()
		ticket = &epoch
	}
	go func() {
		defer cancel()
		var data Payload
		var err error
		if client == nil && makeClient != nil {
			client, err = makeClient()
		}
		if err == nil {
			data, err = loadPayload(ctx, client, fetch, project, dataset)
		}
		result.data = data
		dispatch.Dispatch(func() {
			if result.request != g.LoadRequest {
				return
			}
			g.Loading = false
			if !g.currentLoad(result) {
				return
			}
			if err != nil {
				g.Error = err.Error()
				var apiErr *api.APIError
				if g.Fence != nil && errors.As(err, &apiErr) && (apiErr.IsUnauthorized() || apiErr.IsForbidden()) {
					g.Fence.InvalidateReading()
				}
				return
			}
			if g.AssetsCheck != nil && g.Host == nil {
				g.PendingLoad, g.Loading = &result, true
				return
			}
			g.applyLoad(result, ticket)
		})
	}()
}

func loadPayload(ctx context.Context, client *api.Client, fetch RawFetcher, p shell.ProjectRef, dataset string) (Payload, error) {
	if client != nil {
		resp, err := client.Graph(ctx, p.ID, dataset, "")
		if err == nil {
			if resp.Nodes == nil || resp.Edges == nil {
				return Payload{}, fmt.Errorf("官方图数据结构不匹配")
			}
			if resp.DatasetID != "" && resp.DatasetID != dataset {
				return Payload{}, fmt.Errorf("官方图数据集身份不匹配")
			}
			return NormalizeGraphResponse(resp)
		}
		var apiErr *api.APIError
		if fetch == nil || !errors.As(err, &apiErr) || !apiErr.IsNotFound() {
			return Payload{}, err
		}
	}
	if fetch == nil {
		return Payload{}, fmt.Errorf("官方图服务不可用")
	}
	raw, err := fetch(ctx, p, dataset)
	if err != nil {
		return Payload{}, err
	}
	return DecodePayload(raw)
}

func (g *Controller) currentLoad(result LoadResult) bool {
	return result.request == g.LoadRequest && (g.Fence == nil || result.fence.Valid(g.Fence))
}

func (g *Controller) applyLoad(result LoadResult, ticket *uint64) {
	if g.Host == nil {
		g.Error = "官方图组件不可用"
		return
	}
	if err := g.Host.ApplyParsed(result.project, result.dataset, result.data, ticket); err != nil {
		g.Error = err.Error()
		return
	}
	if g.Selection != "" {
		var selected struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal([]byte(g.Selection), &selected)
		g.Selection = string(g.Host.Node(selected.ID))
	}
}

func (g *Controller) ResetView(mode string) {
	g.Mode = mode
	g.Host.Clear()
	g.Selection, g.Error = "", ""
	g.CancelLoad()
}

func (g *Controller) CancelLoad() {
	g.LoadRequest++
	g.PendingLoad, g.Loading = nil, false
	if g.cancelLoad != nil {
		g.cancelLoad()
		g.cancelLoad = nil
	}
}

func (g *Controller) InvalidateReading(dataset string) {
	g.CancelLoad()
	if dataset == "" || g.Host == nil || g.Host.Dataset() == "" || g.Host.Dataset() == dataset {
		g.Host.Clear()
		g.Selection = ""
	}
}

func (g *Controller) CodeIndexBlocked() bool {
	return g.CodeBusy || g.CodeUnknown || g.CodeRemoteActive
}

func (g *Controller) project() (shell.ProjectRef, bool) {
	if g.CurrentProject == nil {
		return shell.ProjectRef{}, false
	}
	return g.CurrentProject()
}

func flag(fn func() bool) bool { return fn != nil && fn() }
