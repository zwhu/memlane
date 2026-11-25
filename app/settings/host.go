package settings

import (
	"io/fs"
	"net/http"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/app/shell"
)

// Host supplies the desktop-owned services used by Settings. Dispatcher queues
// background results on the UI thread. Router and Theme provide navigation and
// appearance for the window presenting this controller.
type Host struct {
	Dispatcher    shell.Dispatcher
	DispatchReady func() bool
	Router        shell.Router
	Session       shell.SessionFence
	Theme         shell.ThemeProvider
	HTTP          *http.Client

	// The composition root injects the embedded installer and runtime files.
	BootstrapAssets fs.FS
	RuntimeAssets   fs.FS
	BrandMark       ui.ImageSource

	WindowHidden    func() bool
	WindowInactive  func() bool
	OverviewPending func() bool

	ConfigChanged       func(Config)
	RuntimeConfigLoaded func(Config)
	RuntimeReady        func()

	ProjectsChanged         func(previous, current Config)
	CurrentProject          func() (Project, bool)
	EditProject             func(projectID string)
	OpenProjectManagement   func(projectID string)
	BaseReadingIdentity     func() string
	ReadingPolicyChanged    func(revision string)
	InvalidateDataset       func(datasetID string)
	KnowledgeCatalogChanged func(projectID, datasetID, version string)
	UnreadWritePending      func(projectID, datasetID string) bool

	// The graph controller owns code indexing. ProjectCodeStatus reports the
	// settings phase, which prepares the dataset and verifies its permissions.
	ReloadGraphAssets func(dir string)
	RefreshGraph      func()
	IndexProjectCode  func(Project)
	ProjectCodeBusy   func() bool
	ProjectCodeStatus func(busy bool, status, problem string)

	ChooseProjectFolder  func() (string, error)
	ChooseDiagnosticPath func() (string, error)
	OpenExistingAgent    func(string) error
	OpenExternalURL      func(string) error

	Appearance         func() string
	SetAppearance      func(string)
	KeepAliveOnQuit    func() bool
	SetKeepAliveOnQuit func(bool)
	RefreshOpenAtLogin func()
	OpenAtLogin        func() bool
	SetOpenAtLogin     func(bool) error
	ToggleOpenAtLogin  func(bool)
	OpenAtLoginError   func() string

	RenderVersionUpdate   func(*ui.Context)
	RenderServiceOverview func(*ui.Context)
	ServiceConnected      func() bool
}
