package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"log"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/desktop"
	"github.com/zwhu/memlane/internal/platform"
)

func main() {
	var win *mygo.Window
	var app *desktop.App
	var controller *desktop.RuntimeController
	settings := desktop.RuntimeSettings{KeepService: true}
	var quitPending, forceQuit, stopRequested bool
	var availabilityEpoch atomic.Uint64
	stopAndQuit := func() { stopRequested = true; mygo.App.Quit() }
	configPath, action := os.Getenv("MEMLANE_DEV_RUNTIME_CONFIG"), ""
	for i, arg := range os.Args {
		if arg == "--p0-config" && i+1 < len(os.Args) {
			configPath = os.Args[i+1]
		}
		if arg == "--p0-action" && i+1 < len(os.Args) {
			action = os.Args[i+1]
		}
	}
	if isolatedVisualFixtureEnabled() {
		configPath = ""
	}
	mygo.Theme.SetSource(mygo.ThemeLight)
	fileMenu := []*mygo.MenuItem{
		{Label: "搜索所有记忆", Accelerator: "CmdOrCtrl+K", Click: func(*mygo.MenuItem, *mygo.Window) {
			if app != nil && win != nil {
				win.Update(app.OpenGlobalSearch)
				mygo.App.Focus()
				win.Show()
				win.Focus()
			}
		}},
		{Label: "打开 Memlane", Click: func(*mygo.MenuItem, *mygo.Window) {
			if win != nil {
				mygo.App.Focus()
				win.Show()
				win.Focus()
			}
		}},
		{Label: "关闭窗口", Accelerator: "CmdOrCtrl+W", Click: func(*mygo.MenuItem, *mygo.Window) {
			if win != nil {
				win.Close()
			}
		}},
		{Label: "停止服务并退出", Click: func(*mygo.MenuItem, *mygo.Window) { stopAndQuit() }},
	}
	fileMenu = appendNativeCaptureMenu(fileMenu, func() *mygo.Window { return win })
	mygo.App.SetMenu(mygo.NewMenu([]*mygo.MenuItem{
		{Role: mygo.RoleAppMenu},
		{Label: "文件", Submenu: fileMenu},
		{Role: mygo.RoleEditMenu},
		{Label: "视图", Submenu: []*mygo.MenuItem{{Role: mygo.RoleToggleFullScreen}}},
		{Role: mygo.RoleWindowMenu},
	}))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var quitting bool
	var previewQuitPending, previewQuitFlushed bool
	mygo.App.OnBeforeQuit(func(e *mygo.QuitEvent) {
		if app != nil && app.HasDraftPreview() && !previewQuitFlushed {
			e.PreventDefault()
			if previewQuitPending {
				return
			}
			previewQuitPending = true
			app.FlushDraftPreviews(func(err error) {
				previewQuitPending = false
				if err != nil {
					log.Printf("退出前草稿保存未完成：%v", err)
					return
				}
				previewQuitFlushed = true
				mygo.App.Quit()
			})
			return
		}
		if controller != nil && (stopRequested || !settings.KeepService) && !forceQuit {
			e.PreventDefault()
			if quitPending {
				return
			}
			quitPending = true
			controller.Cancel()
			app.SetRuntimeStatus(desktop.RuntimeStatus{Phase: "preparing", Message: "正在停止记忆服务…"})
			go func() {
				stopCtx, stop := context.WithTimeout(context.Background(), 20*time.Second)
				defer stop()
				err := controller.StopService(stopCtx)
				win.Update(func() {
					quitPending = false
					if err != nil {
						previewQuitFlushed = false
						app.CancelExperiencePreviewExit()
						app.SetRuntimeStatus(desktop.RuntimeStatus{Phase: "failed", Message: "停止失败，请重试：" + err.Error()})
						return
					}
					forceQuit = true
					mygo.App.Quit()
				})
			}()
			return
		}
		quitting = true
		cancel()
	})
	mygo.App.WhenReady(func() {
		version := mygo.App.Version()
		if version == "" {
			version = "开发版"
		}
		var installing bool
		installUpdate := func(update *mygo.Update) {
			if installing || quitPending {
				return
			}
			installing = true
			availabilityEpoch.Add(1)
			app.SetUpdateStatus("正在准备更新…", true)
			updateCtx, stop := context.WithTimeout(ctx, 5*time.Minute)
			app.SetCancelUpdate(func() {
				app.SetCancelUpdate(nil)
				app.SetUpdateStatus("正在取消更新，等待回执…", true)
				stop()
			})
			go func() {
				defer stop()
				wasRunning := false
				var err error
				if controller != nil {
					wasRunning, err = controller.QuiesceForUpdate(updateCtx)
				}
				if err == nil {
					err = update.Install(updateCtx, func(done, total int64) {
						if ctx.Err() == nil {
							win.Update(func() {
								if total > 0 && done >= total {
									app.SetCancelUpdate(nil)
									app.SetUpdateStatus("正在校验并安装更新…", true)
								} else if updateCtx.Err() == nil {
									app.SetCancelUpdate(func() {
										app.SetCancelUpdate(nil)
										app.SetUpdateStatus("正在取消更新，等待回执…", true)
										stop()
									})
									app.SetUpdateStatus(fmt.Sprintf("正在下载更新 %.2f / %.2f MB", float64(done)/1e6, float64(total)/1e6), true)
								}
							})
						}
					})
				}
				if err != nil {
					cancelled, restored := errors.Is(err, context.Canceled) || errors.Is(updateCtx.Err(), context.Canceled), true
					if wasRunning && controller != nil {
						restoreCtx, restoreStop := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
						defer restoreStop()
						resumeErr := controller.Prepare(restoreCtx, func(status desktop.RuntimeStatus) {
							if ctx.Err() == nil {
								win.Update(func() { app.SetRuntimeStatus(status) })
							}
						})
						if resumeErr != nil {
							restored = false
							err = fmt.Errorf("%w；后台恢复失败：%v", err, resumeErr)
						}
					}
					message := "更新失败，请重试：" + err.Error()
					if cancelled && restored {
						message = "更新已取消，当前安装与知识保留。"
					}
					if ctx.Err() == nil {
						win.Update(func() { installing = false; app.SetCancelUpdate(nil); app.SetUpdateStatus(message, false) })
					}
					return
				}
				win.Update(func() { app.SetCancelUpdate(nil); forceQuit = true; mygo.App.Relaunch() })
			}()
		}
		app = desktop.New(version, func() {
			if !mygo.Updater.Enabled() {
				app.SetUpdateStatus("此版本尚未启用自动更新", false)
				return
			}
			app.SetUpdateStatus("正在检查更新…", true)
			go func() {
				checkCtx, stop := context.WithTimeout(ctx, desktop.UpdateTimeout)
				defer stop()
				update, err := mygo.Updater.Check(checkCtx)
				message := "已是最新版本"
				if err != nil {
					message = "检查失败，请稍后重试：" + err.Error()
				} else if update != nil {
					message = "发现新版本 " + update.Version
				}
				if ctx.Err() == nil {
					win.Update(func() {
						app.SetUpdateStatus(message, false)
						if err == nil && update != nil {
							app.SetInstallUpdate(func() { installUpdate(update) })
						} else {
							app.SetInstallUpdate(nil)
						}
					})
				}
			}()
		})
		app.ConfigureWindowActions(func() { mygo.App.Quit() }, stopAndQuit, func() {
			if win != nil {
				win.Hide()
			}
		})
		verificationWidth, verificationHeight := verificationWindowSize()
		win = mygo.NewWindow(mygo.WindowOptions{
			Title: "Memlane", Width: verificationWidth, Height: verificationHeight,
			MinWidth: 800, MinHeight: 620,
			TitleBarStyle: mygo.TitleBarHidden,
			Vibrancy:      mygo.VibrancySidebar,
			Content:       ui.View(app.View),
		})
		configureNativeProbe(win, app)
		app.ConfigureProjectDirectoryChooser(func() (string, error) {
			paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: win, Title: "选择本地 Git 仓库", ButtonLabel: "选择文件夹", Directory: true})
			if err != nil || len(paths) == 0 {
				return "", err
			}
			return paths[0], nil
		})
		win.OnHide(func() { app.SetWindowVisible(false) })
		win.OnShow(func() { app.SetWindowVisible(true) })
		configureVisualFixture(app)
		configureNativeReading(ctx, app, win)
		if configPath == "" && !isolatedVisualFixtureEnabled() {
			if resources, err := mygo.App.Path(mygo.PathResources); err == nil {
				candidate := filepath.Join(resources, "runtime-config.json")
				if _, err = os.Stat(candidate); err == nil {
					configPath = candidate
				}
			}
		}
		if configPath != "" {
			cfg, err := desktop.LoadRuntimeConfig(configPath)
			if err != nil {
				app.SetRuntimeStatus(desktop.RuntimeStatus{Phase: "failed", Message: "本地运行配置不可用：" + err.Error()})
			} else {
				controller = &desktop.RuntimeController{Config: cfg}
				app.ConfigureBackupRecovery(controller.RestoreBackup, func() {
					// A new UI process prevents pre-restore IPC callbacks and drafts
					// from being reused across the database/authorization epoch.
					forceQuit = true
					mygo.App.Relaunch()
				})
				app.ConfigureGuidance(cfg.Root)
				if e := desktop.ConfigureControlledUpdates(cfg); e != nil {
					app.SetUpdateStatus("更新源配置失败："+e.Error(), false)
				}
				if loaded, e := desktop.LoadRuntimeSettings(cfg.Root); e == nil {
					settings = loaded
				} else {
					app.SetRuntimeStatus(desktop.RuntimeStatus{Phase: "failed", Message: "设置读取失败：" + e.Error()})
				}
				var latest desktop.RuntimeStatus
				notify := func(status desktop.RuntimeStatus) {
					switch status.Phase {
					case "ready", "stopped", "authorization", "failed", "cancelled":
						// Discard observations captured during quiescence before
						// an update rollback or lifecycle operation completed.
						availabilityEpoch.Add(1)
					}
					latest = status
					if ctx.Err() == nil {
						win.Update(func() { app.SetRuntimeStatus(status) })
					}
				}
				prepare := func() {
					if quitPending || quitting {
						return
					}
					app.SetRuntimeStatus(desktop.RuntimeStatus{Phase: "checking", Message: "正在检查本地组件…"})
					availabilityEpoch.Add(1)
					go func() {
						err := controller.Prepare(ctx, notify)
						if err != nil && ctx.Err() == nil {
							if errors.Is(err, context.Canceled) {
								notify(desktop.RuntimeStatus{Phase: "cancelled", Message: "准备已取消，可以继续准备"})
							} else {
								notify(desktop.RuntimeStatus{Phase: "failed", Message: "准备失败，请重试：" + err.Error()})
							}
						}
					}()
				}
				stopService := func() {
					availabilityEpoch.Add(1)
					app.SetRuntimeStatus(desktop.RuntimeStatus{Phase: "preparing", Message: "正在停止记忆服务…"})
					go func() {
						stopCtx, stop := context.WithTimeout(ctx, 20*time.Second)
						defer stop()
						err := controller.StopService(stopCtx)
						if err != nil {
							notify(desktop.RuntimeStatus{Phase: "failed", Message: err.Error()})
						} else {
							notify(desktop.RuntimeStatus{Phase: "stopped", Message: "记忆服务已主动停止", Installed: true})
						}
					}()
				}
				resumeService := func() {
					if quitPending || quitting {
						return
					}
					availabilityEpoch.Add(1)
					app.SetRuntimeStatus(desktop.RuntimeStatus{Phase: "checking", Message: "正在启动记忆服务…"})
					go func() {
						if err := controller.ResumeService(ctx, notify); err != nil {
							notify(desktop.RuntimeStatus{Phase: "failed", Message: err.Error()})
						}
					}()
				}
				app.ConfigureMaintenanceRecovery(controller.ObserveAvailability, platform.OpenBackgroundServiceSettings, func() { win.Show(); win.Focus() })
				app.ConfigureRuntime(prepare, controller.Cancel, stopService, resumeService, settings.KeepService, func(keep bool) {
					settings.KeepService = keep
					if err := desktop.SaveRuntimeSettings(cfg.Root, settings); err != nil {
						app.SetRuntimeStatus(desktop.RuntimeStatus{Phase: "failed", Message: "设置保存失败：" + err.Error()})
					}
				})
				if action != "" {
					go func() {
						var err error
						switch action {
						case "prepare":
							err = controller.Prepare(ctx, notify)
						case "status":
							latest.Service, err = platform.ServiceStatus(cfg.ServicePlist)
						case "register":
							err = platform.RegisterService(cfg.ServicePlist)
							latest.Service, _ = platform.ServiceStatus(cfg.ServicePlist)
						case "unregister":
							err = platform.UnregisterService(cfg.ServicePlist)
							latest.Service, _ = platform.ServiceStatus(cfg.ServicePlist)
						case "stop":
							err = controller.StopService(ctx)
						case "resume":
							err = controller.ResumeService(ctx, notify)
						case "install":
							var update *mygo.Update
							update, err = mygo.Updater.Check(ctx)
							if err == nil && update == nil {
								err = fmt.Errorf("no newer test update")
							}
							wasRunning := false
							if err == nil {
								wasRunning, err = controller.QuiesceForUpdate(ctx)
							}
							if err == nil {
								err = update.Install(ctx, func(done, total int64) {
									notify(desktop.RuntimeStatus{Phase: "updating", Message: "正在下载客户端更新", Downloaded: done, Total: total})
								})
							}
							if err != nil && wasRunning {
								if e := controller.Prepare(ctx, notify); e != nil {
									err = fmt.Errorf("%w; restore: %v", err, e)
								}
							}
							if err == nil {
								latest.Phase = "update_installed"
							}
						default:
							err = fmt.Errorf("unknown P0 action")
						}
						result := map[string]any{"action": action, "phase": latest.Phase, "system_state": latest.Service}
						if err != nil {
							result["error"] = err.Error()
						}
						_ = json.NewEncoder(os.Stdout).Encode(result)
						win.Update(func() { forceQuit = true; mygo.App.Quit() })
					}()
				} else {
					if os.Getenv("MEMLANE_DEV_RUNTIME_CONFIG") != "" || !app.ConfigureFirstLaunch(ctx, controller, func(fn func()) {
						if ctx.Err() == nil {
							win.Update(fn)
						}
					}) {
						prepare()
					}
					// Configure the startup gate before scheduling page data reads.
					app.ConfigureLibrary(ctx, filepath.Join(cfg.Root, "data"), func(fn func()) {
						if ctx.Err() == nil {
							win.Update(fn)
						}
					})
					go func() {
						ticker := time.NewTicker(2 * time.Second)
						defer ticker.Stop()
						for {
							select {
							case <-ctx.Done():
								return
							case <-ticker.C:
							}
							epoch := availabilityEpoch.Load()
							observe, stop := context.WithTimeout(ctx, 1500*time.Millisecond)
							status, err := controller.ObserveAvailability(observe)
							stop()
							if errors.Is(err, desktop.ErrRuntimeChanging) || ctx.Err() != nil {
								continue
							}
							if err != nil {
								status = desktop.RuntimeAvailability{Phase: "unavailable", Message: "无法确认后台状态；已保存的原文保留，请重试。"}
							}
							win.Update(func() {
								if epoch == availabilityEpoch.Load() && !installing && !quitPending && !quitting {
									app.ApplyRuntimeAvailability(status)
								}
							})
						}
					}()
				}
			}
		}
		show := func() { mygo.App.Focus(); win.Show(); win.Focus() }
		if action == "" && !isolatedVisualFixtureEnabled() {
			home, _ := os.UserHomeDir()
			clientRoot := filepath.Join(home, "Library/Application Support/Memlane")
			if controller != nil {
				clientRoot = controller.Config.Root
			}
			resources, _ := mygo.App.Path(mygo.PathResources)
			bridge := filepath.Join(resources, "bin/memlane")
			if os.Getenv("MEMLANE_DEV_RUNTIME_CONFIG") != "" && os.Getenv("MEMLANE_DEV_BRIDGE") != "" {
				bridge = os.Getenv("MEMLANE_DEV_BRIDGE")
			}
			piDir := os.Getenv("PI_CODING_AGENT_DIR")
			if piDir == "" {
				piDir = filepath.Join(home, ".pi/agent")
			}
			app.ConfigureAgents(ctx, clientRoot, home, bridge, piDir, func(fn func()) {
				if ctx.Err() == nil {
					win.Update(fn)
				}
			})
			show()
		}
		if isolatedVisualFixtureEnabled() {
			show()
		}
		_, err := mygo.NewTray(mygo.TrayOptions{
			Icon: trayIcon(), IconIsTemplate: true, ToolTip: "Memlane",
			Menu: mygo.NewMenu([]*mygo.MenuItem{
				{Label: "打开 Memlane", Click: func(*mygo.MenuItem, *mygo.Window) { show() }},
				mygo.Separator(), {Label: "退出 Memlane", Role: mygo.RoleQuit},
				{Label: "停止服务并退出", Click: func(*mygo.MenuItem, *mygo.Window) { stopAndQuit() }},
			}),
		})
		if err != nil {
			log.Printf("菜单栏不可用，关闭窗口将退出：%v", err)
		} else {
			win.OnClose(func(e *mygo.CloseEvent) {
				if !quitting {
					e.PreventDefault()
					win.Hide()
				}
			})
		}
		mygo.App.OnActivate(func(visible bool) {
			if !visible {
				show()
			}
		})
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

func trayIcon() []byte {
	im := image.NewNRGBA(image.Rect(0, 0, 32, 32))
	for y := 7; y < 25; y++ {
		for x := 6; x < 26; x++ {
			if x < 9 || x > 22 || (y-7 == (x-6)/2 && x < 16) || (y-7 == (25-x)/2 && x >= 16) {
				im.Set(x, y, color.Black)
			}
		}
	}
	var b bytes.Buffer
	_ = png.Encode(&b, im)
	return b.Bytes()
}
