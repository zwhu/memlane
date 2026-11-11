package desktop

import "github.com/egoist/mygo/ui"

func (a *App) projectEditor(c *ui.Context) {
	p := &a.projectCreation
	ui.Scroll(c.Key("project-creation-body")).Grow(1).MinHeight(0).TrackScroll(&p.Scroll).Padding(24).Gap(18).Children(func() {
		ui.Text(c, "本地仓库").FontSize(10).TextColor(design.Muted)
		ui.Text(c, "添加一个项目").FontSize(design.Title).Bold()
		ui.Text(c, "为代码仓库建立记忆空间，之后可以在这里查看知识与原始记录。").FontSize(12).LineHeight(1.7).TextColor(design.Muted)
		ui.Text(c, "项目名称").FontSize(11).TextColor(design.Muted)
		ui.TextInput(c.Key("project-name"), &a.projectName).Label("项目名称").Placeholder("例如：API 客户端").Disabled(a.projectBusy).OnChange(func() { p.NameEdited = true })
		ui.Text(c, "仓库位置").FontSize(11).TextColor(design.Muted)
		ui.Row(c).Wrap().Gap(8).Children(func() {
			ui.TextInput(c.Key("project-path"), &a.projectPath).Label("仓库位置").Placeholder("选择本地 Git 仓库").Grow(1).MinWidth(140).Disabled(a.projectBusy).OnChange(a.projectCreationPathChanged)
			quietButton(c, "选择文件夹").Key("project-choose-directory").Disabled(p.Picker == nil || a.projectBusy).OnClick(a.chooseProjectDirectory)
		})
		quietButton(c, "检查仓库").Key("project-inspect").AlignSelf(ui.Start).Disabled(p.Checking || a.projectBusy || a.projectPath == "").OnClick(a.checkProjectRepository)
		if p.Checking {
			ui.Text(c, "正在检查仓库…").FontSize(11).TextColor(design.Muted)
		}
		if a.projectError != "" {
			ui.Text(c, a.projectError).FontSize(11).TextColor(design.Muted)
		}
		if p.Inspection != nil && p.CheckedPath == a.projectPath {
			ui.Text(c, projectInspectionMessage(*p.Inspection)).FontSize(11).Selectable()
			if p.Inspection.Existing != nil {
				ui.Text(c, p.Inspection.Existing.Name).FontSize(12).Bold()
			}
		}
		ui.Text(c, "同一个仓库的多个工作目录共用项目知识。").FontSize(11).TextColor(design.Muted)
	})
	ui.Row(c).Shrink(0).Padding(12, 24).Gap(8).BorderWidth(1, 0, 0, 0).BorderColor(design.Border).Children(func() {
		ui.Text(c, "添加后可单独管理 Agent 的访问权限").FontSize(10).TextColor(design.Muted).Grow(1).MinWidth(0)
		if p.Inspection != nil && p.Inspection.State == "existing" {
			actionButton(c, "打开已有项目", nil).Key("project-open-existing").Disabled(a.projectBusy || p.CheckedPath != a.projectPath || p.Inspection.Existing == nil).OnClick(a.openInspectedProject)
		} else {
			actionButton(c, "添加项目", nil).Key("register-project").Disabled(a.onProject == nil || a.projectBusy || !a.projectCreationCanRegister()).OnClick(func() {
				if a.onProject != nil && a.projectCreationCanRegister() {
					a.onProject()
				}
			})
		}
	})
}
