package desktop

import "github.com/egoist/mygo/ui"

func (a *App) projectEditor(c *ui.Context) {
	ui.Column(c).Grow(1).Padding(24).Gap(18).Children(func() {
		ui.Text(c, "添加本地项目").FontSize(design.Title).Bold()
		ui.Text(c, "一个项目对应一个代码仓库，worktree 共用记忆。添加后可以在 Agent 页面连接共享记忆。").FontSize(design.Small).LineHeight(1.7).TextColor(design.Muted)
		ui.TextInput(c.Key("project-name"), &a.projectName).Label("项目名称").Placeholder("项目名称")
		ui.TextInput(c.Key("project-path"), &a.projectPath).Label("仓库路径").Placeholder("本地 Git 仓库的绝对路径")
		if a.projectError != "" {
			ui.Text(c, a.projectError).FontSize(design.Small).TextColor(design.Muted)
		}
		actionButton(c.Key("register-project"), "添加项目", icons["plus"]).AlignSelf(ui.Start).Disabled(a.onProject == nil || a.projectBusy).OnClick(func() {
			if a.onProject != nil {
				a.onProject()
			}
		})
	})
}
