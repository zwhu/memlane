//go:build e2e

package desktop

import "context"

// Configures only the explicit isolated client already owning this fixture's
// installation. Normal settings callbacks perform every consent operation.
func (a *App) OpenNativePiCaptureSettingsVerification(ctx context.Context, clientRoot, home, bridge, piDirectory, connection string, dispatch func(func())) {
	a.ConfigureAgents(ctx, clientRoot, home, bridge, piDirectory, dispatch)
	a.navigate("设置", "")
	a.selectSettingsSection("agents")
	a.agents.Selection = "connection-" + connection
}

func (a *App) NativePiCaptureVerificationState() map[string]any {
	if a.page != "设置" || a.settingsSection != "agents" {
		return a.NativeReadingVerificationState()
	}
	out := map[string]any{"mode": "pi-capture-settings", "selection": a.agents.Selection}
	if s := a.agents.PiCapture; s != nil {
		out["loaded"], out["loading"], out["busy"], out["sequence"], out["rows"] = s.Loaded, s.Loading, s.Busy, s.Sequence, len(s.Rows)
		out["review_pending"] = s.Review != nil
		statuses := []map[string]any{}
		for _, r := range s.Rows {
			enabled := r.Consent != nil && r.Consent.Enabled
			statuses = append(statuses, map[string]any{"project": r.Binding.Project, "ready": r.Ready, "enabled": enabled, "saved": r.Saved})
		}
		out["projects"] = statuses
	}
	return out
}
