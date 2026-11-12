//go:build e2e

package desktop

import (
	"context"
	"github.com/zwhu/memlane/internal/official"
)

func (a *App) OpenNativeOfficialPluginVerification(ctx context.Context, root, home, connection string, controller *official.Controller, dispatch func(func())) {
	a.firstLaunch.Controller = &RuntimeController{Config: RuntimeConfig{Root: root}, Official: controller}
	a.ConfigureAgents(ctx, root, home, "", "", dispatch)
	a.navigate("设置", "")
	a.selectSettingsSection("agents")
	a.agents.Selection = "connection-" + connection
}
