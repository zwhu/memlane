//go:build e2e

package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/egoist/mygo"
	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/desktop"
	"github.com/zwhu/memlane/internal/official"
)

// Explicitly marked isolated host, with real client IPC and signed components.
// It neither installs a plugin nor simulates a successful connection.
func configureNativeOfficialPlugin(ctx context.Context, app *desktop.App, win *mygo.Window) {
	path := os.Getenv("MEMLANE_NATIVE_PLUGIN_CONFIG")
	var cfg struct {
		SyntheticOnly                        bool `json:"synthetic_only"`
		Root, Home, Connection, OfficialRoot string
		Official                             official.Config
	}
	raw, err := os.ReadFile(path)
	if err == nil && len(raw) < 8192 {
		err = json.Unmarshal(raw, &cfg)
	}
	id, idErr := uuid.Parse(cfg.Connection)
	if err != nil || len(raw) >= 8192 || idErr != nil || id.String() != cfg.Connection || !cfg.SyntheticOnly || !filepath.IsAbs(path) || filepath.Clean(path) != filepath.Join(cfg.Root, "native-plugin-probe.json") || !filepath.IsAbs(cfg.Root) || !filepath.IsAbs(cfg.OfficialRoot) || cfg.Home != filepath.Join(filepath.Dir(cfg.Root), "host") {
		app.SetRuntimeStatus(desktop.RuntimeStatus{Phase: "failed", Message: "隔离插件验证配置无效"})
		return
	}
	dispatch := func(fn func()) {
		if ctx.Err() == nil {
			win.Update(fn)
		}
	}
	app.ConfigureLibrary(ctx, cfg.Root, dispatch)
	app.OpenNativeOfficialPluginVerification(ctx, cfg.Root, cfg.Home, cfg.Connection, &official.Controller{Root: cfg.OfficialRoot, Config: cfg.Official}, dispatch)
}
