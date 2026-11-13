package simpledesktop

import "github.com/egoist/mygo/ui"

func (a *App) projectAddMenu(c *ui.Context) {
	readingAction(c, "新建项目", commonPlus, func() { a.openProjectCreation("") })
}
