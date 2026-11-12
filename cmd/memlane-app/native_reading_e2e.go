//go:build e2e

package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/egoist/mygo"
	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/agents"
	"github.com/zwhu/memlane/internal/desktop"
)

var nativeCaptureDiagnostics func() map[string]any
var nativeCopyExpected func(string) (string, bool)

// This is an explicit isolated-service UI verification entry, not a runtime
// installer. No fixture rows, credentials or Agent host paths are supplied.
func configureNativeReading(ctx context.Context, app *desktop.App, win *mygo.Window) {
	if isolatedVisualFixtureEnabled() && os.Getenv("MEMLANE_MVP_VISUAL_STATE") == "conversation-plugin-live" {
		configureNativeOfficialPlugin(ctx, app, win)
		return
	}
	if !isolatedVisualFixtureEnabled() || os.Getenv("MEMLANE_MVP_VISUAL_STATE") != "conversation-live" {
		return
	}
	path := os.Getenv("MEMLANE_NATIVE_READING_CONFIG")
	var cfg struct {
		SyntheticOnly             bool `json:"synthetic_only"`
		Root, Project             string
		CaptureSettingsConnection string `json:"capture_settings_connection,omitempty"`
	}
	data, err := os.ReadFile(path)
	id, parseErr := uuid.Parse(cfg.Project)
	if err == nil && len(data) < 4096 && filepath.IsAbs(path) {
		err = json.Unmarshal(data, &cfg)
		id, parseErr = uuid.Parse(cfg.Project)
	}
	if err != nil || parseErr != nil || id.String() != cfg.Project || !cfg.SyntheticOnly || !filepath.IsAbs(cfg.Root) || filepath.Clean(path) != filepath.Join(cfg.Root, "native-reading-probe.json") {
		app.SetRuntimeStatus(desktop.RuntimeStatus{Phase: "failed", Message: "隔离阅读验证配置无效"})
		return
	}
	app.ConfigureLibrary(ctx, cfg.Root, func(fn func()) {
		if ctx.Err() == nil {
			win.Update(fn)
		}
	})
	nativeCopyExpected = app.NativeReadingCopyExpected
	if cfg.CaptureSettingsConnection != "" {
		connection, err := uuid.Parse(cfg.CaptureSettingsConnection)
		clientRoot := filepath.Dir(cfg.Root)
		installation, loadErr := agents.LoadInstallation(clientRoot, cfg.CaptureSettingsConnection)
		if err != nil || connection.String() != cfg.CaptureSettingsConnection || loadErr != nil || installation.Connection.Agent != "pi" {
			app.SetRuntimeStatus(desktop.RuntimeStatus{Phase: "failed", Message: "隔离pi设置验证配置无效"})
			return
		}
		app.OpenNativePiCaptureSettingsVerification(ctx, clientRoot, filepath.Join(filepath.Dir(clientRoot), "agent-home"), installation.Options.Bridge, installation.Options.PiDirectory, cfg.CaptureSettingsConnection, func(fn func()) {
			if ctx.Err() == nil {
				win.Update(fn)
			}
		})
		nativeCaptureDiagnostics = app.NativePiCaptureVerificationState
		return
	}
	app.OpenNativeReadingVerification(cfg.Project)
	nativeCaptureDiagnostics = app.NativeReadingVerificationState
}
