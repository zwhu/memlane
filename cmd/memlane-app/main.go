package main

import (
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/simpledesktop"
	"log"
	"os"
)

func main() {
	cfg, err := simpledesktop.LoadConfig(os.Getenv("MEMLANE_DEV_RUNTIME_CONFIG"))
	// Each UI process verifies and loads the current official graph asset bundle.
	app := simpledesktop.New(cfg, err)
	mygo.Theme.SetSource(mygo.ThemeLight)
	mygo.App.OnReady(func() {
		win := mygo.NewWindow(mygo.WindowOptions{Title: "Memlane", Width: 1200, Height: 820, MinWidth: 1200, MinHeight: 820, TitleBarStyle: mygo.TitleBarHidden, Content: ui.View(app.View)})
		app.Update = func(fn func()) { win.Update(fn) }
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
