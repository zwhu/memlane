package main

import (
	"bytes"
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/bootstrap"
	"github.com/zwhu/memlane/internal/simpledesktop"
)

func main() {
	configuration := os.Getenv("MEMLANE_DEV_RUNTIME_CONFIG")
	firstDefault := configuration == ""
	var pathError error
	if firstDefault {
		configuration, pathError = bootstrap.DefaultRuntimeConfig()
	}
	cfg, err := simpledesktop.LoadConfig(configuration)
	if pathError != nil {
		err = pathError
	}
	cfg.FirstInstallDefault = firstDefault
	// Each UI process verifies and loads the current official graph asset bundle.
	app := simpledesktop.New(cfg, err)
	// Visual V4 follows the system appearance unless overridden by the saved
	// in-app preference or dev-only MEMLANE_APPEARANCE / appearance file.
	mygo.Theme.SetSource(appearanceSource(configuration, app.AppearancePreference()))
	app.ApplyAppearanceSource = func(mode string) {
		mygo.Theme.SetSource(themeSourceForMode(mode))
	}
	mygo.App.OnReady(func() {
		win := mygo.NewWindow(mygo.WindowOptions{
			Title:         "Memlane",
			Width:         1200,
			Height:        820,
			MinWidth:      1200,
			MinHeight:     820,
			Hidden:        true,
			TitleBarStyle: mygo.TitleBarHidden,
			Vibrancy:      mygo.VibrancySidebar,
			Content:       ui.View(app.View),
		})

		quitCoord := newQuitCoordinator(
			app.KeepAliveOnQuit,
			func(ctx context.Context) error {
				app.FlushPendingState(2 * time.Second)
				err := bootstrap.ShutdownAllFirstRuntimes(ctx)
				if configuration != "" {
					if extErr := bootstrap.ShutdownExternalRuntime(ctx, configuration); extErr != nil {
						err = errors.Join(err, extErr)
					}
				}
				return err
			},
			mygo.App.Quit,
			defaultShutdownTimeout,
			log.Printf,
		)
		mygo.App.OnBeforeQuit(func(e *mygo.QuitEvent) {
			quitCoord.HandleBeforeQuit(e.PreventDefault)
		})

		const (
			trayFlyoutWidth  = 300
			trayFlyoutHeight = 480
		)

		initialIcon, initialTemplate, initialTip := app.TrayIconState()
		tray, trayErr := mygo.NewTray(mygo.TrayOptions{
			ID:             "memlane-tray",
			Icon:           initialIcon,
			IconIsTemplate: initialTemplate,
			ToolTip:        initialTip,
		})
		if trayErr != nil {
			log.Printf("菜单栏状态图标不可用：%v", trayErr)
		}
		mygo.App.OnWillQuit(func(*mygo.QuitEvent) {
			app.FlushPendingState(2 * time.Second)
			if tray != nil {
				tray.Destroy()
			}
		})

		var flyout *mygo.Window
		var lastIcon []byte
		var lastTemplate bool
		var lastTip string
		if tray != nil {
			lastIcon = initialIcon
			lastTemplate = initialTemplate
			lastTip = initialTip
			app.SetTrayDark(tray.Dark())
		}
		ensureFlyout := func() *mygo.Window {
			if tray == nil {
				return nil
			}
			if flyout != nil && !flyout.IsDestroyed() {
				return flyout
			}
			f := mygo.NewFlyout(mygo.FlyoutOptions{
				Tray:         tray,
				Placement:    mygo.PlacementBottom,
				Gap:          4,
				Width:        trayFlyoutWidth,
				Height:       trayFlyoutHeight,
				Focusable:    true,
				Shadow:       true,
				CornerRadius: 10,
				Hidden:       true,
				Content:      ui.View(app.TrayFlyoutView),
			})
			// Closing by blur/outside click/Escape would destroy a focusable flyout
			// and break subsequent tray clicks unless prevented and hidden instead.
			f.OnClose(func(event *mygo.CloseEvent) {
				if !quitCoord.IsQuitting() {
					event.PreventDefault()
					f.Hide()
				}
			})
			flyout = f
			return f
		}

		syncTrayIcon := func() {
			if tray == nil {
				return
			}
			pngBytes, isTemplate, tip := app.TrayIconState()
			if !bytes.Equal(pngBytes, lastIcon) || isTemplate != lastTemplate {
				if err := tray.SetIcon(pngBytes, isTemplate); err == nil {
					lastIcon = pngBytes
					lastTemplate = isTemplate
				}
			}
			if tip != lastTip {
				tray.SetToolTip(tip)
				lastTip = tip
			}
		}

		if tray != nil {
			syncTrayIcon()
			tray.OnClick(func() {
				app.SetTrayDark(tray.Dark())
				syncTrayIcon()
				f := ensureFlyout()
				if f == nil {
					win.Show()
					win.Focus()
					return
				}
				if f.IsVisible() {
					f.Hide()
					return
				}
				app.RefreshOpenAtLogin()
				// Re-anchor against the live clicked tray icon bounds (handles
				// initial layout, icon shifts, and multi-monitor menu bars).
				f.SetSize(trayFlyoutWidth, trayFlyoutHeight)
				f.Update(func() {})
				f.Show()
			})
			tray.OnAppearanceChange(func() {
				app.SetTrayDark(tray.Dark())
				syncTrayIcon()
				if flyout != nil && !flyout.IsDestroyed() && flyout.IsVisible() {
					flyout.Update(func() {})
				}
			})
		}

		app.Update = func(fn func()) {
			win.Update(func() {
				if fn != nil {
					fn()
				}
				syncTrayIcon()
			})
			if flyout != nil && !flyout.IsDestroyed() && flyout.IsVisible() {
				flyout.Update(func() {})
			}
		}
		app.ShowMainWindowFn = func() {
			win.Update(func() {})
			win.Show()
			win.Focus()
		}
		app.OpenGlobalSearchFn = func() {
			win.Update(app.OpenSearch)
			win.Show()
			win.Focus()
		}
		app.HideTrayFlyoutFn = func() {
			if flyout != nil && !flyout.IsDestroyed() {
				flyout.Hide()
			}
		}
		app.SyncTrayIconFn = syncTrayIcon
		app.ReadOpenAtLoginFn = func() bool {
			return mygo.App.OpenAtLogin()
		}
		app.SetOpenAtLoginFn = func(enable bool) error {
			return mygo.App.SetOpenAtLogin(enable)
		}

		app.QuitAppFn = quitCoord.RequestQuit

		app.ChooseDiagnosticPath = func() (string, error) {
			return mygo.Dialog.Save(mygo.SaveDialogOptions{Parent: win, Title: "导出安装诊断", DefaultPath: "Memlane-diagnostic.zip", ButtonLabel: "导出", Filters: []mygo.FileFilter{{Name: "ZIP", Extensions: []string{"zip"}}}})
		}
		app.OpenExistingAgent = func(path string) error { return mygo.Shell.OpenPath(path) }
		app.OpenExternalURL = func(raw string) error { return mygo.Shell.OpenExternal(raw) }
		app.ChooseProjectFolder = func() (string, error) {
			paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: win, Title: "选择源文件夹", ButtonLabel: "添加", Directory: true})
			if err != nil || len(paths) == 0 {
				return "", err
			}
			return paths[0], nil
		}
		// Keep the reading tree alive when the user closes the main window.
		// An explicit app quit must still close it so dev reloads can finish.
		win.OnClose(func(event *mygo.CloseEvent) {
			if !quitCoord.IsQuitting() {
				event.PreventDefault()
				win.Hide()
			}
		})
		win.OnFocus(func() { win.Update(func() { app.SetWindowActive(true) }) })
		win.OnBlur(func() { win.Update(func() { app.SetWindowActive(false) }) })
		win.OnHide(func() { win.Update(func() { app.SetWindowActive(false) }) })
		if err := app.AttachGraph(win); err != nil {
			log.Printf("官方图组件不可用：%v", err)
		}
		mygo.App.SetMenu(mygo.NewMenu([]*mygo.MenuItem{
			{Role: mygo.RoleAppMenu},
			{Label: "文件", Submenu: []*mygo.MenuItem{
				{Label: "搜索记忆", Accelerator: "CmdOrCtrl+K", Click: func(*mygo.MenuItem, *mygo.Window) { win.Update(app.OpenSearch); win.Show(); win.Focus() }},
				{Label: "打开 Memlane", Click: func(*mygo.MenuItem, *mygo.Window) { win.Show(); win.Focus() }},
				{Label: "关闭窗口", Accelerator: "CmdOrCtrl+W", Click: func(*mygo.MenuItem, *mygo.Window) { win.Close() }},
			}},
			{Role: mygo.RoleEditMenu},
			{Role: mygo.RoleWindowMenu},
		}))
		mygo.App.OnActivate(func(visible bool) {
			if !visible {
				win.Show()
				win.Focus()
			}
		})

		app.RefreshOpenAtLogin()
		if !mygo.App.WasOpenedAtLogin() {
			win.Show()
		}
		app.Start()
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

// appearanceSource is read at startup; dev overrides take precedence over the
// persisted in-app preference so both appearances can be checked quickly.
func appearanceSource(configuration, saved string) mygo.ThemeSource {
	value := os.Getenv("MEMLANE_APPEARANCE")
	if value == "" && configuration != "" {
		if data, err := os.ReadFile(filepath.Join(filepath.Dir(configuration), "appearance")); err == nil {
			value = string(data)
		}
	}
	if strings.TrimSpace(value) == "" {
		value = saved
	}
	return themeSourceForMode(value)
}

func themeSourceForMode(value string) mygo.ThemeSource {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "light":
		return mygo.ThemeLight
	case "dark":
		return mygo.ThemeDark
	}
	return mygo.ThemeSystem
}
