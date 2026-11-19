package simpledesktop

import (
	"github.com/egoist/mygo/ui"
	"path/filepath"
	"strings"
)

func (a *App) projectCreateDialog(c *ui.Context) {
	s := &a.projectCreate
	if !s.Open {
		return
	}
	w, h := c.Size()
	ui.Modal(c.Key("project-editor-modal"), &s.Open, func() {
		ui.Row(c).FillWidth().Padding(0, 0, 8, 0).AlignItems(ui.Center).Children(func() {
			title := "创建项目"
			if s.TargetProject != "" {
				title = "编辑项目"
			}
			homeText(c, title, homeHeading).FontSize(20)
			ui.Spacer(c)
			readingAction(c, "关闭项目弹框", readingClose, func() { s.Open = false })
		})
		ui.Scroll(c.Key("project-create-content")).Width(min(float32(480), w-120)).MaxHeight(h - 260).MinHeight(0).Gap(18).Children(func() {
			ui.Field(c, "项目名称", func() {
				input := ui.TextInput(c.Key("create-project-name"), &s.Name).Label("项目名称").Placeholder("项目名称").FillWidth().Disabled(s.Saving || s.Submitted || (s.TargetProject == "" && s.Selected != nil && s.Selected.Kind == "project"))
				if s.Focus {
					input.Focus()
					s.Focus = false
				}
			})
			if s.TargetProject == "" {

				if s.Selected == nil {
					ui.Column(c).Gap(10).Children(func() {
						homeText(c, "源文件夹", homeHeading).FontSize(13)
						zone := ui.Column(c).FillWidth().MinHeight(116).Padding(18).Gap(12).Border(1, homeBorder).Shadow(0, 2, 8, 0, panelShadowInk).Radius(10).Background(ui.Hex("#f8fafc")).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
							if s.Folder == "" {
								homeText(c, "选择或拖入这台电脑上的项目文件夹", homeMeta)
								textButton(c, "添加文件夹", nil).OnClick(a.selectProjectFolder).Disabled(s.Saving || s.Submitted || a.ChooseProjectFolder == nil)
							} else {
								ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(12).Children(func() {
									ui.Icon(c, homeFolder).FontSize(24).TextColor(homeMuted)
									ui.Column(c).Grow(1).MinWidth(0).Gap(6).Children(func() {
										homeText(c, filepath.Base(s.Folder), homeHeading).FontSize(13)
										homeText(c, s.Folder, homePath).SingleLine().Tooltip(s.Folder)
									})
									readingAction(c, "移除源文件夹", readingClose, func() { s.Folder = "" }).Disabled(s.Saving || s.Submitted)
								})
							}
						})
						if !s.Saving && !s.Submitted {
							if zone.FileDragOver() {
								zone.BorderColor(c.Theme().Accent)
							}
							if files := zone.DroppedFiles(); len(files) > 0 {
								a.acceptProjectFolderDrop(files)
							}
						}
					})
				}
				if s.Selected != nil {
					ui.Row(c).FillWidth().Padding(12).Gap(10).Background(ui.Hex("#edf3f9")).Radius(7).Children(func() {
						ui.Column(c).Grow(1).MinWidth(0).Gap(5).Children(func() {
							homeText(c, "将关联已有项目记录", homeMeta)
							homeText(c, s.Selected.Name, homeBody).FontSize(12).SingleLine()
						})
						if s.TargetProject == "" {
							projectLink(c, "取消选择", func() { s.Selected = nil; s.Name = "" }).Disabled(s.Saving || s.Submitted)
						}
					})
				} else if s.TargetProject == "" {
					ui.Column(c).Gap(10).Children(func() {
						ui.Row(c).AlignItems(ui.Center).Children(func() {
							homeText(c, "已有项目记录", homeHeading).FontSize(12)
							ui.Spacer(c)
							if s.Loading {
								homeText(c, "读取中…", homeMeta)
							}
						})
						if s.SuggestionError != "" {
							ui.Row(c).Gap(12).Children(func() {
								homeText(c, s.SuggestionError, homeMeta)
								refreshAction(c, "重新读取", a.loadProjectSuggestions)
							})
						}
						if s.Loaded && len(s.Suggestions) == 0 && s.SuggestionError == "" {
							homeText(c, "没有发现尚未关联的项目", homeMeta)
						}
						for _, row := range s.Suggestions {
							suggestion := row
							ui.Button(c, "").Label("建议关联："+suggestion.Name).FillWidth().MinWidth(0).Padding(12).Background(ui.Hex("#f8fafc")).Border(1, homeBorder).Radius(7).Gap(10).Children(func() {
								ui.Icon(c, homeFolder).FontSize(17).TextColor(homeMuted)
								ui.Column(c).Grow(1).MinWidth(0).Gap(5).Children(func() {
									homeText(c, suggestion.Name, homeBody).FontSize(12).SingleLine()
									homeText(c, "已有记录，尚未加入项目列表", homeMeta)
								})
								queueBadge(c, "建议关联", homeLinkInk)
							}).Disabled(s.Saving || s.Submitted).OnClick(func() { s.Selected = &suggestion; s.Name = suggestion.Name; s.Folder = "" })
						}
					})
				}
			}
			if s.Message != "" {
				homeText(c, s.Message, homeMeta).MaxLines(3)
			}
		})
		ui.Row(c).FillWidth().Padding(12, 0, 0, 0).Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Spacer(c)
			textButton(c, "取消", nil).OnClick(func() { s.Open = false })
			if s.Submitted && !s.Saving {
				ui.PrimaryButton(c, "核对保存结果").OnClick(a.confirmProjectCreation)
			} else {
				label := "创建项目"
				if s.Selected != nil {
					label = "关联项目"
				}
				if s.TargetProject != "" {
					label = "保存名称"
				}
				if s.Saving {
					label = "保存中…"
				}
				ui.PrimaryButton(c, label).OnClick(a.submitProjectCreation).Disabled(s.Saving || s.Submitted || strings.TrimSpace(s.Name) == "" || (s.TargetProject == "" && s.Selected == nil && s.Folder == "") || !a.access.Loaded)
			}
		})
	}).Label("项目编辑弹框").Transition(projectOverlayMotion())
}
