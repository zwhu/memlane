package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/app"
	"github.com/zwhu/memlane/internal/bootstrap"
	"github.com/zwhu/memlane/kit/daemon"
)

const appInstanceLockFilename = ".app-instance.lock"

type appInstanceLock struct {
	mu    sync.Mutex
	files []*os.File
}

func lockSingleInstanceFile(lockPath string) (*os.File, bool, error) {
	if linfo, statErr := os.Lstat(lockPath); statErr == nil {
		if linfo.Mode()&os.ModeSymlink != 0 || !linfo.Mode().IsRegular() {
			return nil, false, errors.New("app instance lock must be a regular file")
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return nil, false, statErr
	}
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, false, err
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return nil, false, err
	}
	if err := daemon.Flock(file, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return file, true, nil
}

func canonicalPathForLockKey(path string) string {
	cleaned := filepath.Clean(path)
	if resolved, err := filepath.EvalSymlinks(cleaned); err == nil {
		if abs, absErr := filepath.Abs(resolved); absErr == nil {
			return filepath.Clean(abs)
		}
		return filepath.Clean(resolved)
	}
	parent := filepath.Dir(cleaned)
	if parent != cleaned {
		if resolvedParent, err := filepath.EvalSymlinks(parent); err == nil {
			joined := filepath.Join(resolvedParent, filepath.Base(cleaned))
			if abs, absErr := filepath.Abs(joined); absErr == nil {
				return filepath.Clean(abs)
			}
			return filepath.Clean(joined)
		}
	}
	if abs, err := filepath.Abs(cleaned); err == nil {
		return filepath.Clean(abs)
	}
	return cleaned
}

// acquireAppInstanceLock acquires dual-anchor non-blocking locks for a store:
//  1. <configDir>/.app-instance-<sha256(storeRoot)[:16]>.lock (always acquired,
//     even before first-install creates library/), and
//  2. <storeRoot>/.app-instance.lock (acquired whenever storeRoot already exists).
//
// Different stores remain completely isolated while .app and --config targeting
// the same store share the same anchor before and after first-install setup.
func acquireAppInstanceLock(configuration, storeRoot string) (*appInstanceLock, bool, error) {
	storeRoot = strings.TrimSpace(storeRoot)
	configuration = strings.TrimSpace(configuration)
	if storeRoot == "" && configuration == "" {
		return nil, true, nil
	}
	lock := &appInstanceLock{}
	rollback := func() { _ = lock.Release() }

	if configuration != "" && storeRoot != "" {
		configDir := filepath.Dir(configuration)
		if err := os.MkdirAll(configDir, 0o700); err != nil {
			return nil, false, err
		}
		resolvedConfigDir, err := filepath.EvalSymlinks(configDir)
		if err != nil {
			return nil, false, err
		}
		key := canonicalPathForLockKey(storeRoot)
		sum := sha256.Sum256([]byte(key))
		anchorPath := filepath.Join(resolvedConfigDir, ".app-instance-"+hex.EncodeToString(sum[:8])+".lock")
		f, acquired, err := lockSingleInstanceFile(anchorPath)
		if err != nil {
			return nil, false, err
		}
		if !acquired {
			return nil, false, nil
		}
		lock.files = append(lock.files, f)
	}

	if storeRoot != "" {
		resolved, err := filepath.EvalSymlinks(storeRoot)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				if len(lock.files) == 0 {
					return nil, true, nil
				}
				return lock, true, nil
			}
			rollback()
			return nil, false, err
		}
		info, err := os.Stat(resolved)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				if len(lock.files) == 0 {
					return nil, true, nil
				}
				return lock, true, nil
			}
			rollback()
			return nil, false, err
		}
		if !info.IsDir() {
			rollback()
			return nil, false, errors.New("app instance lock store root must be a directory")
		}
		f, acquired, err := lockSingleInstanceFile(filepath.Join(resolved, appInstanceLockFilename))
		if err != nil {
			rollback()
			return nil, false, err
		}
		if !acquired {
			rollback()
			return nil, false, nil
		}
		lock.files = append(lock.files, f)
	}

	if len(lock.files) == 0 {
		return nil, true, nil
	}
	return lock, true, nil
}

func (l *appInstanceLock) Release() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	var errs []error
	for i := len(l.files) - 1; i >= 0; i-- {
		f := l.files[i]
		if f == nil {
			continue
		}
		errs = append(errs, daemon.Flock(f, syscall.LOCK_UN), f.Close())
	}
	l.files = nil
	return errors.Join(errs...)
}

func resolveAppStoreRoot(configuration, runtimeStoreRoot, officialData string, firstDefault bool) string {
	if strings.TrimSpace(runtimeStoreRoot) != "" {
		return runtimeStoreRoot
	}
	if strings.TrimSpace(officialData) != "" {
		return officialData
	}
	if strings.TrimSpace(configuration) != "" {
		if rt, err := readRuntime(configuration); err == nil && strings.TrimSpace(rt.StoreRoot) != "" {
			return rt.StoreRoot
		}
		if firstDefault {
			// Match internal/bootstrap/first_runtime.go:579 deterministic default library root.
			return filepath.Join(filepath.Dir(configuration), "library")
		}
		return filepath.Dir(configuration)
	}
	return ""
}

func shutdownAppRuntimeOnQuit(
	ctx context.Context,
	ownsInstance bool,
	explicitConfig bool,
	configuration string,
	shutdownFirstRuntimes func(context.Context) error,
	stopExplicitOwner func(context.Context, string) error,
	shutdownExternal func(context.Context, string) error,
) error {
	if !ownsInstance {
		return nil
	}
	err := shutdownFirstRuntimes(ctx)
	if explicitConfig {
		err = errors.Join(err, stopExplicitOwner(ctx, configuration))
	} else if configuration != "" {
		err = errors.Join(err, shutdownExternal(ctx, configuration))
	}
	return err
}

func runApp(args []string, stderr io.Writer) int {
	flags := flag.NewFlagSet("app", flag.ContinueOnError)
	flags.SetOutput(stderr)
	config := flags.String("config", "", "runtime configuration")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return 2
	}
	configuration := firstNonempty(*config, os.Getenv("MEMLANE_DEV_RUNTIME_CONFIG"))
	firstDefault := configuration == ""
	var pathError error
	if firstDefault {
		configuration, pathError = bootstrap.DefaultRuntimeConfig()
	}
	cfg, err := app.LoadConfig(configuration)
	if pathError != nil {
		err = pathError
	}
	var runtimeStoreRoot string
	if *config != "" {
		runtime, loadErr := readRuntime(configuration)
		if loadErr != nil {
			return reportError(stderr, loadErr)
		}
		if validateErr := runtime.validateStore(); validateErr != nil {
			return reportError(stderr, validateErr)
		}
		runtimeStoreRoot = runtime.StoreRoot
		if cfg.ProjectControl == nil {
			cfg.ProjectControl = &app.ProjectControlConfig{}
		}
		cfg.ProjectControl.URL = runtime.GatewayURL
		cfg.ProjectControl.ManagementTokenFile = runtime.ManagementTokenFile
		if cfg.ServiceURL == "" {
			cfg.ServiceURL = runtime.EngineURL
			err = nil
		}
	}
	// P1 #8: all UI entrypoints targeting the same store share the canonical
	// <storeRoot>/.app-instance.lock so .app and --config launches cannot both
	// open or kill each other's daemon. firstDefault also keeps native macOS
	// single-instance wake-up via RequestSingleInstanceLock + OnSecondInstance.
	if firstDefault {
		if !mygo.App.RequestSingleInstanceLock() {
			return 0
		}
	}
	storeRoot := resolveAppStoreRoot(configuration, runtimeStoreRoot, cfg.OfficialData, firstDefault)
	instanceLock, acquired, lockErr := acquireAppInstanceLock(configuration, storeRoot)
	if lockErr != nil {
		return reportError(stderr, lockErr)
	}
	if !acquired {
		return 0
	}
	ownsInstance := instanceLock != nil
	if instanceLock != nil {
		defer func() { _ = instanceLock.Release() }()
	}
	cfg.FirstInstallDefault = firstDefault
	// Each UI process verifies and loads the current official graph asset bundle.
	desktop := app.New(cfg, err)
	// Visual V4 follows the system appearance unless overridden by the saved
	// in-app preference or dev-only MEMLANE_APPEARANCE / appearance file.
	mygo.Theme.SetSource(appearanceSource(configuration, desktop.AppearancePreference()))
	desktop.ApplyAppearanceSource = func(mode string) {
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
			Content:       ui.View(desktop.View),
		})

		quitCoord := newQuitCoordinator(
			desktop.KeepAliveOnQuit,
			func(ctx context.Context) error {
				desktop.StopTrayBackgroundPolling()
				desktop.StopForegroundPolling()
				if flushErr := desktop.FlushPendingState(2 * time.Second); flushErr != nil {
					log.Printf("退出前保存客户端状态未完成：%v", flushErr)
				}
				return shutdownAppRuntimeOnQuit(
					ctx,
					ownsInstance,
					*config != "",
					configuration,
					bootstrap.ShutdownAllFirstRuntimes,
					func(stopCtx context.Context, cfgPath string) error {
						rt, loadErr := readRuntime(cfgPath)
						if loadErr != nil {
							return loadErr
						}
						return stopOwner(stopCtx, rt)
					},
					bootstrap.ShutdownExternalRuntime,
				)
			},
			mygo.App.Quit,
			defaultShutdownTimeout,
			log.Printf,
		)
		mygo.App.OnBeforeQuit(func(e *mygo.QuitEvent) {
			quitCoord.HandleBeforeQuit(e.PreventDefault)
		})

		initialIcon, initialTemplate, initialTip := desktop.TrayIconState()
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
			desktop.StopTrayBackgroundPolling()
			desktop.StopForegroundPolling()
			if flushErr := desktop.FlushPendingState(2 * time.Second); flushErr != nil {
				log.Printf("退出前保存客户端状态未完成：%v", flushErr)
			}
			if tray != nil {
				tray.Destroy()
			}
		})

		var flyout *mygo.Window
		var flyoutHeight int
		var lastIcon []byte
		var lastTemplate bool
		var lastTip string
		if tray != nil {
			lastIcon = initialIcon
			lastTemplate = initialTemplate
			lastTip = initialTip
			desktop.SetTrayDark(tray.Dark())
		}
		ensureFlyout := func() *mygo.Window {
			if tray == nil {
				return nil
			}
			if flyout != nil && !flyout.IsDestroyed() {
				return flyout
			}
			flyoutHeight = desktop.TrayFlyoutHeight()
			f := mygo.NewFlyout(mygo.FlyoutOptions{
				Tray:         tray,
				Placement:    mygo.PlacementBottom,
				Gap:          4,
				Width:        app.TrayFlyoutWidth,
				Height:       flyoutHeight,
				Focusable:    true,
				Shadow:       true,
				CornerRadius: 10,
				Hidden:       true,
				Content:      ui.View(desktop.TrayFlyoutView),
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
			pngBytes, isTemplate, tip := desktop.TrayIconState()
			if !bytes.Equal(pngBytes, lastIcon) || isTemplate != lastTemplate {
				if err := tray.SetIcon(pngBytes, isTemplate); err == nil {
					lastIcon = pngBytes
					lastTemplate = isTemplate
				} else {
					log.Printf("更新菜单栏图标失败：%v", err)
				}
			}
			if tip != lastTip {
				tray.SetToolTip(tip)
				lastTip = tip
			}
		}

		// Revalidate reading credentials and flush withdrawn UI state before revealing a retained window.
		showMainWindow := func(action func()) {
			win.Update(func() {
				desktop.SetWindowHidden(false)
				if action != nil {
					action()
				}
			})
			win.Update(func() {
				win.Show()
				win.Focus()
			})
		}

		if tray != nil {
			syncTrayIcon()
			tray.OnClick(func() {
				desktop.SetTrayDark(tray.Dark())
				syncTrayIcon()
				f := ensureFlyout()
				if f == nil {
					showMainWindow(nil)
					return
				}
				if f.IsVisible() {
					f.Hide()
					return
				}
				desktop.RefreshOpenAtLoginAsync()
				desktop.RefreshForTrayClick()
				// Re-anchor against the live clicked tray icon bounds (handles
				// initial layout, icon shifts, and multi-monitor menu bars).
				flyoutHeight = desktop.TrayFlyoutHeight()
				f.SetSize(app.TrayFlyoutWidth, flyoutHeight)
				f.Update(func() {})
				f.Show()
			})
			tray.OnAppearanceChange(func() {
				desktop.SetTrayDark(tray.Dark())
				syncTrayIcon()
				if flyout != nil && !flyout.IsDestroyed() && flyout.IsVisible() {
					flyout.Update(func() {})
				}
			})
		}

		desktop.Update = func(fn func()) {
			win.Update(func() {
				if fn != nil {
					fn()
				}
				syncTrayIcon()
				if flyout != nil && !flyout.IsDestroyed() && flyout.IsVisible() {
					if h := desktop.TrayFlyoutHeight(); h != flyoutHeight {
						flyoutHeight = h
						flyout.SetSize(app.TrayFlyoutWidth, h)
					}
					flyout.Update(func() {})
				}
			})
		}
		desktop.ShowMainWindowFn = func() { showMainWindow(nil) }
		desktop.OpenGlobalSearchFn = func() { showMainWindow(desktop.OpenSearch) }
		desktop.HideTrayFlyoutFn = func() {
			if flyout != nil && !flyout.IsDestroyed() {
				flyout.Hide()
			}
		}
		desktop.SyncTrayIconFn = syncTrayIcon
		desktop.ReadOpenAtLoginFn = func() bool {
			return mygo.App.OpenAtLogin()
		}
		desktop.SetOpenAtLoginFn = func(enable bool) error {
			return mygo.App.SetOpenAtLogin(enable)
		}

		desktop.QuitAppFn = quitCoord.RequestQuit

		desktop.ChooseDiagnosticPath = func() (string, error) {
			return mygo.Dialog.Save(mygo.SaveDialogOptions{Parent: win, Title: "导出安装诊断", DefaultPath: "Memlane-diagnostic.zip", ButtonLabel: "导出", Filters: []mygo.FileFilter{{Name: "ZIP", Extensions: []string{"zip"}}}})
		}
		desktop.OpenExistingAgent = func(path string) error { return mygo.Shell.OpenPath(path) }
		desktop.OpenExternalURL = func(raw string) error { return mygo.Shell.OpenExternal(raw) }
		desktop.ChooseProjectFolder = func() (string, error) {
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
		win.OnFocus(func() {
			win.Update(func() {
				desktop.SetWindowActive(true)
				desktop.OnWindowFocus()
			})
		})
		win.OnBlur(func() { win.Update(func() { desktop.SetWindowActive(false) }) })
		win.OnHide(func() { win.Update(func() { desktop.SetWindowHidden(true) }) })
		desktop.BindGraphWindow(win)
		mygo.App.SetMenu(mygo.NewMenu([]*mygo.MenuItem{
			{Role: mygo.RoleAppMenu},
			{Label: "文件", Submenu: []*mygo.MenuItem{
				{Label: "搜索记忆", Accelerator: "CmdOrCtrl+K", Click: func(*mygo.MenuItem, *mygo.Window) { desktop.OpenGlobalSearchFn() }},
				{Label: "打开 Memlane", Click: func(*mygo.MenuItem, *mygo.Window) { desktop.ShowMainWindowFn() }},
				{Label: "关闭窗口", Accelerator: "CmdOrCtrl+W", Click: func(*mygo.MenuItem, *mygo.Window) { win.Close() }},
			}},
			{Role: mygo.RoleEditMenu},
			{Role: mygo.RoleWindowMenu},
		}))
		mygo.App.OnActivate(func(visible bool) {
			if !visible || !win.IsVisible() {
				desktop.ShowMainWindowFn()
			}
		})
		if firstDefault {
			mygo.App.OnSecondInstance(func(_ []string, _ string) {
				desktop.ShowMainWindowFn()
			})
		}

		if shouldStartHiddenAtLogin(mygo.App.WasOpenedAtLogin(), wasOpenedAtLoginAtReady()) {
			desktop.SetWindowHidden(true)
		} else {
			win.Show()
		}
		desktop.Start()
		desktop.StartForegroundPolling()
		if tray != nil {
			desktop.StartTrayBackgroundPolling()
		}
	})
	if err := mygo.App.Run(); err != nil {
		reportError(stderr, err)
		return 1
	}
	return 0
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
