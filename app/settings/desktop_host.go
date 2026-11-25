package settings

import "net/http"

func (a *Controller) canDispatch() bool {
	return a.dispatch != nil && (a.host.DispatchReady == nil || a.host.DispatchReady())
}

// UseTransport updates the desktop's runtime and HTTP client before starting work.
func (a *Controller) UseTransport(config Config, http *http.Client) {
	changed := a.client.Config.ServiceURL != config.ServiceURL || a.client.Config.TokenFile != config.TokenFile
	a.config, a.client.Config, a.client.HTTP = config, config, http
	if changed {
		a.refreshBaseIdentity()
	}
}

func (a *Controller) ProjectAccessState() *ProjectAccessState   { return &a.access }
func (a *Controller) ServiceMonitorState() *ServiceMonitorState { return &a.monitor }
func (a *Controller) IntegrationState() *IntegrationState       { return &a.integrations }
func (a *Controller) EnvironmentState() *EnvironmentSetupState  { return &a.environment }
func (a *Controller) SetupDeferred() bool                       { return a.setupDeferredMode }
func (a *Controller) ToolGraphOpen() bool                       { return a.toolGraph.Open }
func (a *Controller) TraySummaryComputations() uint64           { return a.trayCache.summaryComputations }
func (a *Controller) ResetTraySummaryCache()                    { a.trayCache = trayFlyoutCache{} }

func (a *Controller) SetInitialSetupForHost(initial bool) { a.initialSetup = initial }
