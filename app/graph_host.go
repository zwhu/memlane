package app

import (
	"io/fs"
	"sync"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane"
	"github.com/zwhu/memlane/app/api"
	appgraph "github.com/zwhu/memlane/app/graph"
	"github.com/zwhu/memlane/app/shell"
)

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

func (a *App) reloadGraphAssetsAfterOwnerSwitch(dir string) {
	a.configureGraphTransport()
	a.graphController.ReloadAssets(dir, a.page == "图谱")
}
