package main

import (
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/bootstrap"
	"github.com/zwhu/memlane/internal/simpledesktop"
	"log"
	"os"
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
	mygo.Theme.SetSource(mygo.ThemeLight)
	mygo.App.OnReady(func() {
		win := mygo.NewWindow(mygo.WindowOptions{Title: "Memlane", Width: 1200, Height: 820, MinWidth: 1200, MinHeight: 820, TitleBarStyle: mygo.TitleBarHidden, Content: ui.View(app.View)})
		app.Update = func(fn func()) { win.Update(fn) }
		app.ChooseDiagnosticPath = func() (string, error) {
			return mygo.Dialog.Save(mygo.SaveDialogOptions{Parent: win, Title: "导出安装诊断", DefaultPath: "Memlane-diagnostic.zip", ButtonLabel: "导出", Filters: []mygo.FileFilter{{Name: "ZIP", Extensions: []string{"zip"}}}})
		}
		app.OpenExistingAgent = func(path string) error { return mygo.Shell.OpenPath(path) }
		app.ChooseProjectFolder = func() (string, error) {
			paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: win, Title: "选择源文件夹", ButtonLabel: "添加", Directory: true})
			if err != nil || len(paths) == 0 {
				return "", err
			}
			return paths[0], nil
		}
		// Keep the reading tree alive when the user closes the main window.
		// An explicit app quit must still close it so dev reloads can finish.
		quitting := false
		mygo.App.OnBeforeQuit(func(*mygo.QuitEvent) { quitting = true })
		win.OnClose(func(event *mygo.CloseEvent) {
			if !quitting {
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
		mygo.App.SetMenu(mygo.NewMenu([]*mygo.MenuItem{{Role: mygo.RoleAppMenu}, {Label: "文件", Submenu: []*mygo.MenuItem{{Label: "搜索知识", Accelerator: "CmdOrCtrl+K", Click: func(*mygo.MenuItem, *mygo.Window) { win.Update(app.OpenSearch); win.Show(); win.Focus() }}, {Label: "打开 Memlane", Click: func(*mygo.MenuItem, *mygo.Window) { win.Show(); win.Focus() }}, {Label: "关闭窗口", Accelerator: "CmdOrCtrl+W", Click: func(*mygo.MenuItem, *mygo.Window) { win.Close() }}}}, {Role: mygo.RoleEditMenu}, {Role: mygo.RoleWindowMenu}}))
		mygo.App.OnActivate(func(visible bool) {
			if !visible {
				win.Show()
				win.Focus()
			}
		})
		win.Show()
		app.Start()
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
