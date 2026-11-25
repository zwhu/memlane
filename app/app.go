package app

import (
	"context"
	"strings"
	"sync/atomic"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"github.com/zwhu/memlane/app/reading"
	"github.com/zwhu/memlane/app/settings"
)

var ink = lightPalette.Text1
var muted = lightPalette.Text3
var border = lightPalette.Border
var blue = lightPalette.Fill1

type pane struct {
	Items                          []Item
	Selected                       *Item
	Offset                         int
	More, Loaded, Loading, Reading bool
	ReadingSource                  bool
	Error                          string
	Query                          string
	ListScroll                     ui.ScrollState
	Knowledge                      knowledgeFeedState
	RawCatalog                     []Item
	RawV2Mode                      bool
	RawNextCursor                  string
	RawAppending                   bool
	CommonV2Mode                   bool
	CommonNav                      []v2KnowledgePageOpts
	CommonCursors                  []string
	CommonPageIndex                int
	CommonNextCursor               string
	CommonPrevCursor               string
	CommonHasPrev                  bool
	CommonTotalFiltered            int
	WidgetList                     ui.ListState
	ListSelection                  int
	DetailScroll                   map[string]*ui.ScrollState
	DetailLists                    map[string]*ui.ListState
	Tools                          map[string]bool
	History                        []Item
	HomeAnchor                     *Item
	HomeOffset                     int
	HomeCategory                   string
	Locating                       bool
	RevealItem                     string
	SourcePage                     *SessionHistoryPage
	SourceReturn                   *readingLocation
	SessionTools                   bool
	SessionSubagents               bool
	SubagentParent                 *Item
	SubagentParentPage             *SessionHistoryPage
	SessionLoadMaxY                float32
	request                        uint64
	readRequest                    uint64
	readCancel                     context.CancelFunc
	cancel                         context.CancelFunc
	titleCancel                    context.CancelFunc
	titleCtx                       context.Context
	titleRequest                   uint64
	Technical                      map[string]bool
}
type App struct {
	searchController
	graphController
	readingController
	overviewController

	readingInvalidators []namedReadingInvalidator

	userProfiles          userProfilesState
	layout                appLayoutState
	projectUI             projectReadingState
	settingsOrigin        *readingLocation
	access                *settings.ProjectAccessState
	commonEdit            commonEditorState
	commonPage            commonKnowledgePageState
	commonGeneration      uint64
	ChooseProjectFolder   func() (string, error)
	ChooseDiagnosticPath  func() (string, error)
	OpenExistingAgent     func(string) error
	OpenExternalURL       func(string) error
	ApplyAppearanceSource func(string)
	ReadOpenAtLoginFn     func() bool
	SetOpenAtLoginFn      func(bool) error
	ShowMainWindowFn      func()
	OpenGlobalSearchFn    func()
	HideTrayFlyoutFn      func()
	SyncTrayIconFn        func()
	QuitAppFn             func(stopServices bool)
	appearance            appearanceState
	desktopPrefs          desktopPreferencesState
	updateCheck           updateCheckState

	motionRuns struct {
		projectPage  uint64
		projectList  uint64
		rawPage      uint64
		rawList      uint64
		docScrollKey string
		docEnter     uint64
		docBodyKey   string
		docBodyFade  uint64
	}
	poseEnter               poseEnterTracker
	projectCreate           projectCreateState
	Config                  Config
	Client                  Client
	Update                  func(func())
	page                    string
	project                 int
	kind                    string
	epoch                   uint64
	reduceMotion            bool
	authEpoch               uint64
	monitor                 *settings.ServiceMonitorState
	servicePopover          servicePopoverState
	trayPoll                *trayBackgroundPoller
	settingsController      *settings.Controller
	settingsCodeBusy        bool
	trayCache               trayFlyoutCache
	knowledgeWriter         *reading.ReadWriter
	unreadAuthEpoch         atomic.Uint64
	configError             string
	externalStoreNotice     string
	externalStorePollPaused bool
	md                      markdown.Options
	v4MD                    map[v4MDKey]*markdown.Options
}

type v4MDKey struct {
	dark bool
	size float32
}

func New(c Config, err error) *App {
	a := &App{Config: c, Client: Client{Config: c}, page: "总览", kind: "知识"}
	// Cold-start entrance motion is armed atomically once overviewBootstrap
	// delivers the real snapshot (~40-90ms), preventing un-animated flash or double play.
	if c.GenerationSetupOnly {
		a.page = "设置"
	}
	a.initReading(a.Client)
	a.initDesktopPreferences()
	a.loadProjectVisibility()
	a.initOverview()
	a.initSearch()
	a.md = markdown.Options{Theme: &markdown.Theme{FontSize: 12, LineHeight: 1.9, Text: ink, Muted: muted, CodeSize: 11.0 / 12, CodeBlockBackground: lightPalette.Fill1, CodeBlockBorder: border}, CodeBlock: func(c *ui.Context, b *markdown.CodeBlockView) {
		ui.Column(c).Background(tok.Fill1).Border(1, tok.Border).Radius(radius).Children(func() {
			row := ui.Row(c).Padding(6, 12).Gap(4).AlignItems(ui.Center)
			row.Children(func() {
				ui.Text(c, strings.ToUpper(b.Language)).FontSize(10).TextColor(muted)
				hoverCopyAction(c, row, "复制代码", func() { c.WriteClipboard(b.Code()) })
			})
			b.Body(c).Label("代码横向滚动")
		})
	}}
	a.settingsController = settings.New(c, err, a.settingsHost())
	a.access = a.settingsController.ProjectAccessState()
	a.monitor = a.settingsController.ServiceMonitorState()
	a.initGraph(c.GraphAssets)
	if a.settingsController.SetupDeferred() {
		a.page = "总览"
	}
	if err != nil {
		a.configError = err.Error()
	}
	a.ensureReadingInvalidators()
	return a
}
