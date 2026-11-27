package app

import (
	"path/filepath"
	"strings"

	"github.com/egoist/mygo/ui"
)

func (a *App) projectCreateDialog(c *ui.Context) {
	s := &a.projectCreate
	if !s.Open {
		return
	}
	title := "创建项目"
	if s.TargetProject != "" {
		title = "编辑项目"
	}

	confirmText := "创建项目"
	confirmFn := a.submitProjectCreation
	confirmDisabled := s.Saving || s.Submitted || strings.TrimSpace(s.Name) == "" || (s.TargetProject == "" && s.Selected == nil && s.Folder == "") || !a.access.Loaded
	if s.Submitted && !s.Saving {
		confirmText = "核对保存结果"
		confirmFn = a.confirmProjectCreation
		confirmDisabled = false
	} else {
		if s.Selected != nil {
			confirmText = "关联项目"
		}
		if s.TargetProject != "" {
			confirmText = "保存名称"
		}
		if s.Saving {
			confirmText = "保存中…"
		}
	}

	v4Dialog(c, &s.Open, v4DialogOptions{
		Key:             "project-editor-modal",
		Title:           title,
		Label:           "项目编辑弹框",
		Width:           v4DialogLarge,
		ScrollBody:      true,
		CancelText:      "取消",
		ConfirmText:     confirmText,
		ConfirmDisabled: confirmDisabled,
		OnConfirm:       confirmFn,
		Body: func() {
			ui.Column(c).FillWidth().Gap(sp4).Children(func() {
				ui.Field(c, "项目名称", func() {
					input := ui.TextInput(c.Key("create-project-name"), &s.Name).Label("项目名称").Placeholder("项目名称").FillWidth().Disabled(s.Saving || s.Submitted || (s.TargetProject == "" && s.Selected != nil && s.Selected.Kind == "project"))
					if s.Focus {
						input.Focus()
						s.Focus = false
					}
				})
				if s.TargetProject == "" {
					if s.Selected == nil {
						ui.Column(c).FillWidth().Gap(sp2).Children(func() {
							ui.Text(c, "源文件夹").Font("system-ui").FontSize(fsCaption).FontWeight(600).TextColor(tok.Text3)
							zone := ui.Column(c).FillWidth().MinHeight(104).Padding(sp4).Gap(sp3).
								Border(1, tok.Border).Radius(radius).Background(tok.Fill1).
								AlignItems(ui.Center).Justify(ui.Center).Children(func() {
								if s.Folder == "" {
									ui.Text(c, "选择或拖入这台电脑上的项目文件夹").Font("system-ui").FontSize(fsFootnote).TextColor(tok.Text3)
									v4SmallButton(c, "添加文件夹", v4IconPlus, a.selectProjectFolder).Disabled(s.Saving || s.Submitted || a.ChooseProjectFolder == nil)
								} else {
									ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(sp3).Children(func() {
										ui.Icon(c, v4IconFolder).FontSize(20).TextColor(tok.Text2).Shrink(0)
										ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
											ui.Text(c, filepath.Base(s.Folder)).Font("system-ui").FontSize(fsBody).FontWeight(500).TextColor(tok.Text1)
											ui.Text(c, s.Folder).Font("ui-monospace").FontSize(fsCaption).TextColor(tok.Text3).SingleLine().Tooltip(s.Folder)
										})
										v4GhostButton(c, "", v4IconClose, func() { s.Folder = "" }).Label("移除源文件夹").Disabled(s.Saving || s.Submitted)
									})
								}
							})
							if !s.Saving && !s.Submitted {
								if zone.FileDragOver() {
									zone.BorderColor(tok.Accent)
								}
								if files := zone.DroppedFiles(); len(files) > 0 {
									a.acceptProjectFolderDrop(files)
								}
							}
						})
					}
					if s.Selected != nil {
						ui.Row(c).FillWidth().Padding(sp3).Gap(sp3).Background(tok.AccentSofter).
							Border(1, tok.Accent.Alpha(0.25)).Radius(radius).AlignItems(ui.Center).Children(func() {
							ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
								ui.Text(c, "将关联已有项目记录").Font("system-ui").FontSize(fsCaption).TextColor(tok.Text3)
								ui.Text(c, s.Selected.Name).Font("system-ui").FontSize(fsBody).FontWeight(500).TextColor(tok.Text1).SingleLine()
							})
							if s.TargetProject == "" {
								v4SmallButton(c, "取消选择", nil, func() { s.Selected = nil; s.Name = "" }).Disabled(s.Saving || s.Submitted)
							}
						})
					} else if s.TargetProject == "" {
						ui.Column(c).FillWidth().Gap(sp2).Children(func() {
							ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
								ui.Text(c, "已有项目记录").Font("system-ui").FontSize(fsCaption).FontWeight(600).TextColor(tok.Text3)
								ui.Spacer(c)
								if s.Loading {
									ui.Row(c).AlignItems(ui.Center).Gap(5).Children(func() {
										v4Spinner(c)
										ui.Text(c, "读取中…").Font("system-ui").FontSize(fsCaption).TextColor(tok.Text3)
									})
								}
							})
							if s.SuggestionError != "" {
								ui.Row(c).Gap(sp2).AlignItems(ui.Center).Children(func() {
									ui.Text(c, s.SuggestionError).Font("system-ui").FontSize(fsCaption).TextColor(tok.Danger).Grow(1).MinWidth(0)
									v4SmallButton(c, "重新读取", v4IconRefresh, a.loadProjectSuggestions)
								})
							}
							if s.Loaded && len(s.Suggestions) == 0 && s.SuggestionError == "" {
								ui.Text(c, "没有发现尚未关联的项目").Font("system-ui").FontSize(fsFootnote).TextColor(tok.Text3)
							}
							if len(s.Suggestions) > 0 {
								ui.Column(c).FillWidth().Gap(1).Dividers(1, tok.Border).Background(tok.Fill1).Border(1, tok.Border).Radius(radiusMenu).Clip().Children(func() {
									for _, row := range s.Suggestions {
										suggestion := row
										v4Row(c, v4RowOptions{
											Key:         "suggest-" + suggestion.ID,
											Label:       "建议关联：" + suggestion.Name,
											Title:       suggestion.Name,
											Desc:        "已有记录，尚未加入项目列表",
											Clickable:   true,
											HideChevron: true,
											Disabled:    s.Saving || s.Submitted,
											Leading:     func() { ui.Icon(c, v4IconFolder).FontSize(16).TextColor(tok.Text2) },
											Control:     func() { v4Badge(c, "建议关联", "accent") },
											OnClick:     func() { s.Selected = &suggestion; s.Name = suggestion.Name; s.Folder = "" },
										})
									}
								})
							}
						})
					}
				}
				if s.Message != "" {
					ui.Text(c, s.Message).Font("system-ui").FontSize(fsFootnote).TextColor(tok.Text2).MaxLines(3).LineHeight(1.5)
				}
			})
		},
	})
}
