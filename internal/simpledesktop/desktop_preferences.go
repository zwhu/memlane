package simpledesktop

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo/ui"
)

type desktopPreferencesFile struct {
	Version         int  `json:"version"`
	KeepAliveOnQuit bool `json:"keep_alive_on_quit"`
}

type desktopPreferencesState struct {
	loginRefreshSeq uint64
	KeepAliveOnQuit bool
	OpenAtLogin     bool
	LoginError      string
	LoginSynced     bool
	Path            string
	Loaded          bool
	TrayDark        bool
	ForceQuitOpen   bool
}

func (a *App) desktopPreferencesPath() (string, error) {
	base := a.Config.ClientStateFile
	if base == "" {
		base = a.Config.ConfigFile
	}
	if base == "" {
		return "", fmt.Errorf("桌面端运行设置存储位置不可用")
	}
	return filepath.Join(filepath.Dir(base), "desktop-preferences.json"), nil
}

func readDesktopPreferences(path string) bool {
	if path == "" {
		return true
	}
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return true
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return true
	}
	var payload desktopPreferencesFile
	if json.Unmarshal(b, &payload) != nil || payload.Version != 1 {
		return true
	}
	return payload.KeepAliveOnQuit
}

func writeDesktopPreferencesAtomic(path string, b []byte) error {
	if path == "" {
		return fmt.Errorf("桌面端运行设置存储位置不可用")
	}
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("拒绝写入符号链接文件")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".desktop-preferences-*")
	if err != nil {
		return err
	}
	tmpName := f.Name()
	defer os.Remove(tmpName)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		if info, statErr := os.Lstat(path); statErr == nil && info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("拒绝覆盖符号链接文件")
		}
		err = os.Rename(tmpName, path)
	}
	return err
}

// initDesktopPreferences eagerly loads persisted desktop runtime preferences
// during App construction so App.View and TrayFlyoutView never perform disk I/O.
func (a *App) initDesktopPreferences() {
	path, _ := a.desktopPreferencesPath()
	a.desktopPrefs = desktopPreferencesState{
		KeepAliveOnQuit: readDesktopPreferences(path),
		Path:            path,
		Loaded:          true,
	}
}

// KeepAliveOnQuit reports whether quitting the desktop UI keeps background
// services running. Defaults to true.
func (a *App) KeepAliveOnQuit() bool {
	if !a.desktopPrefs.Loaded {
		return true
	}
	return a.desktopPrefs.KeepAliveOnQuit
}

// SetKeepAliveOnQuit updates and atomically persists the keep-alive preference.
func (a *App) SetKeepAliveOnQuit(keepAlive bool) {
	path, err := a.desktopPreferencesPath()
	a.desktopPrefs.KeepAliveOnQuit = keepAlive
	a.desktopPrefs.Path = path
	a.desktopPrefs.Loaded = true
	if err == nil && path != "" {
		if b, marshalErr := json.Marshal(desktopPreferencesFile{Version: 1, KeepAliveOnQuit: keepAlive}); marshalErr == nil {
			_ = writeDesktopPreferencesAtomic(path, b)
		}
	}
}

// RefreshOpenAtLogin synchronizes the cached login-item state from the OS hook.
func (a *App) RefreshOpenAtLogin() {
	a.desktopPrefs.loginRefreshSeq++
	if a.ReadOpenAtLoginFn == nil {
		return
	}
	a.desktopPrefs.OpenAtLogin = a.ReadOpenAtLoginFn()
	a.desktopPrefs.LoginSynced = true
}

func (a *App) RefreshOpenAtLoginAsync() {
	read, update := a.ReadOpenAtLoginFn, a.Update
	if read == nil {
		return
	}
	if update == nil {
		a.RefreshOpenAtLogin()
		return
	}
	a.desktopPrefs.loginRefreshSeq++
	seq := a.desktopPrefs.loginRefreshSeq
	go func() {
		enabled := read()
		update(func() {
			if a.desktopPrefs.loginRefreshSeq == seq {
				a.desktopPrefs.OpenAtLogin = enabled
				a.desktopPrefs.LoginSynced = true
			}
		})
	}()
}

// OpenAtLogin reports whether Memlane is registered to start silently at login.
func (a *App) OpenAtLogin() bool {
	if a.Update == nil && !a.desktopPrefs.LoginSynced && a.ReadOpenAtLoginFn != nil {
		a.RefreshOpenAtLogin()
	}
	return a.desktopPrefs.OpenAtLogin
}

// ToggleOpenAtLogin updates the system login-item state. If the OS rejects the
// request (e.g. macOS SMAppService in a non-.app development binary), it rolls
// back without faking an enabled state and records an honest message in LoginError.
func (a *App) ToggleOpenAtLogin(enable bool) {
	prev := a.OpenAtLogin()
	a.desktopPrefs.loginRefreshSeq++
	if a.SetOpenAtLoginFn == nil {
		a.desktopPrefs.OpenAtLogin = prev
		if enable {
			a.desktopPrefs.OpenAtLogin = false
			a.desktopPrefs.LoginError = "当前运行模式不支持系统登录项（仅 macOS .app 安装包支持），已保持关闭。"
		} else {
			a.desktopPrefs.LoginError = ""
		}
		return
	}
	if err := a.SetOpenAtLoginFn(enable); err != nil {
		if a.ReadOpenAtLoginFn != nil {
			a.desktopPrefs.OpenAtLogin = a.ReadOpenAtLoginFn()
			a.desktopPrefs.LoginSynced = true
		} else {
			a.desktopPrefs.OpenAtLogin = prev
		}
		if enable && a.desktopPrefs.OpenAtLogin {
			a.desktopPrefs.OpenAtLogin = false
		}
		detail := strings.TrimSpace(err.Error())
		if enable {
			if detail != "" {
				a.desktopPrefs.LoginError = fmt.Sprintf("未能开启登录时静默启动（%s），当前保持关闭。", detail)
			} else {
				a.desktopPrefs.LoginError = "未能开启登录时静默启动，当前运行模式可能受 macOS 登录项限制，已保持关闭。"
			}
		} else if detail != "" {
			a.desktopPrefs.LoginError = fmt.Sprintf("未能关闭登录时静默启动（%s）。", detail)
		} else {
			a.desktopPrefs.LoginError = "未能更新系统登录项状态。"
		}
		return
	}
	a.desktopPrefs.LoginError = ""
	if a.ReadOpenAtLoginFn != nil {
		a.desktopPrefs.OpenAtLogin = a.ReadOpenAtLoginFn()
		a.desktopPrefs.LoginSynced = true
	} else {
		a.desktopPrefs.OpenAtLogin = enable
		a.desktopPrefs.LoginSynced = true
	}
}

// QuitApp invokes the host quit callback, optionally stopping owned background services.
func (a *App) QuitApp(stopServices bool) {
	if a.QuitAppFn != nil {
		a.QuitAppFn(stopServices)
	}
}

func (a *App) requestQuit(stopServices bool) {
	if a.HideTrayFlyoutFn != nil {
		a.HideTrayFlyoutFn()
	}
	a.QuitApp(stopServices)
}

func (a *App) openForceQuitConfirm() {
	if a.HideTrayFlyoutFn != nil {
		a.HideTrayFlyoutFn()
	}
	if a.ShowMainWindowFn != nil {
		a.ShowMainWindowFn()
	}
	a.desktopPrefs.ForceQuitOpen = true
}

func (a *App) forceQuitConfirmDialog(c *ui.Context) {
	v4Dialog(c, &a.desktopPrefs.ForceQuitOpen, v4DialogOptions{
		Key:         "force-quit-confirm",
		Title:       "彻底停止并退出 Memlane？",
		Width:       v4DialogSmall,
		Danger:      true,
		ConfirmText: "彻底停止并退出",
		CancelText:  "取消",
		Hint:        "危险操作不响应回车，请手动点击确认",
		Body: func() {
			homeText(c, "将停止本地记忆引擎（Cognee）、接入网关与 Agent 模型代理，正在进行的后台提炼任务会被中断。", homeBody).TextColor(tok.Text1).LineHeight(1.6)
			homeText(c, "已采集的记忆与原始记录保留在本地存储中，下次打开应用时可重新启动服务。", homeMeta).LineHeight(1.5)
		},
		OnConfirm: func() {
			a.desktopPrefs.ForceQuitOpen = false
			a.QuitApp(true)
		},
	})
}
