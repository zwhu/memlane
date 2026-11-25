package app

import "github.com/egoist/mygo/ui"

func (a *App) projectAddMenu(c *ui.Context) {
	readingAction(c, "新建项目", commonPlus, func() { a.openProjectCreation("") }).Size(28, 28).BorderWidth(0).Background(ui.Transparent)
}
