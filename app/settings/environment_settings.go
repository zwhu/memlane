package settings

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/bootstrap"
	kitui "github.com/zwhu/memlane/kit/ui"
)

// Path edits stay local to the dialog until the user explicitly requests detection.
type environmentPathDraft struct {
	Python, UV, Source, Archive, ModelCache, Plugin string
}

func (a *Controller) openEnvironmentPaths() {
	s := &a.environment
	if s.Busy || s.Loading || !s.Loaded {
		return
	}
	s.PathDraft = environmentPathDraft{s.Python, s.UV, s.Source, s.Archive, s.ModelCache, s.Plugin}
	s.PathDialog = true
}

func (a *Controller) cancelEnvironmentPaths() {
	a.environment.PathDialog = false
	a.environment.PathDraft = environmentPathDraft{}
}

func (a *Controller) detectEnvironmentPaths() {
	s := &a.environment
	if s.Busy || s.Loading || !s.PathDialog {
		return
	}
	d := s.PathDraft
	s.Python, s.UV, s.Source, s.Archive, s.ModelCache, s.Plugin = d.Python, d.UV, d.Source, d.Archive, d.ModelCache, d.Plugin
	a.cancelEnvironmentPaths()
	a.detectEnvironment(true)
}

type environmentLocation struct {
	Label, Path string
}

func environmentLocationRows(items []bootstrap.Item) []environmentLocation {
	rows := []environmentLocation{}
	seen := map[string]int{}
	for _, item := range items {
		path := item.Observation.Path
		if path == "" {
			continue
		}
		name := dependencyDisplayName(item.Requirement)
		if index, ok := seen[path]; ok {
			rows[index].Label += " / " + name
			continue
		}
		seen[path] = len(rows)
		rows = append(rows, environmentLocation{name, path})
	}
	return rows
}

func environmentDisplayPath(path string) string {
	home, err := os.UserHomeDir()
	if err == nil && strings.HasPrefix(path, home+string(filepath.Separator)) {
		return "~" + strings.TrimPrefix(path, home)
	}
	return path
}

func (a *Controller) environmentLocations(c *ui.Context) {
	tok := a.tokens()
	s := &a.environment
	rows := environmentLocationRows(s.Plan.Items)
	if len(rows) == 0 {
		return
	}
	ui.Column(c.Key("environment-locations")).FillWidth().Gap(4).Padding(12, 0, 0, 0).BorderWidth(1, 0, 0, 0).BorderColor(tok.Border).Children(func() {
		ui.Row(c).FillWidth().MinHeight(24).AlignItems(ui.Center).Children(func() {
			kitui.Text(c, tok, "使用位置", kitui.RoleMeta).FontWeight(500).TextColor(tok.Text1)
			ui.Spacer(c)
			if s.CopiedPath != "" {
				kitui.Text(c, tok, "路径已复制", kitui.RoleMeta).TextColor(tok.OK)
			}
		})
		for _, location := range rows {
			row := location
			line := ui.Row(c.Key("environment-location-" + row.Path)).FillWidth().MinWidth(0).MinHeight(26).AlignItems(ui.Center).Gap(12)
			line.Children(func() {
				kitui.Text(c, tok, row.Label, kitui.RoleMeta).Width(132).Shrink(0).SingleLine().Tooltip(row.Label)
				kitui.Text(c, tok, environmentDisplayPath(row.Path), kitui.RoleMeta).FontSize(12).TextColor(tok.Text2).Grow(1).MinWidth(0).SingleLine().Tooltip(row.Path)
				kitui.HoverCopyAction(c, tok, line, kitui.IconCopy, "复制"+row.Label+"位置", func() {
					c.WriteClipboard(row.Path)
					s.CopiedPath = row.Path
				})
			})
		}
	})
}

func (a *Controller) environmentPathsDialog(c *ui.Context) {
	tok := a.tokens()
	s := &a.environment
	if !s.PathDialog {
		return
	}
	kitui.Dialog(c, tok, &s.PathDialog, kitui.DialogOptions{
		Key:             "environment-path-dialog",
		Title:           "指定已有路径",
		CloseLabel:      "关闭路径编辑",
		Width:           kitui.DialogLarge,
		ScrollBody:      true,
		CancelText:      "取消",
		ConfirmText:     "检测这些路径",
		Hint:            "路径编辑仅保存在本地，点击检测后才重新核对环境",
		CancelDisabled:  s.Busy || s.Loading,
		ConfirmDisabled: s.Busy || s.Loading,
		OnCancel:        a.cancelEnvironmentPaths,
		OnConfirm:       a.detectEnvironmentPaths,
		Body: func() {
			for _, field := range []struct {
				Label string
				Value *string
			}{
				{"Python 路径", &s.PathDraft.Python},
				{"uv 路径", &s.PathDraft.UV},
				{"Cognee 源码目录", &s.PathDraft.Source},
				{"固定源码归档", &s.PathDraft.Archive},
				{"已有模型缓存目录", &s.PathDraft.ModelCache},
				{"已有 Agent 插件目录", &s.PathDraft.Plugin},
			} {
				ui.Column(c).FillWidth().Gap(4).Children(func() {
					kitui.Text(c, tok, field.Label, kitui.RoleMeta).FontWeight(500).TextColor(tok.Text1)
					ui.TextInput(c.Key("environment-draft-"+field.Label), field.Value).FillWidth().Label("输入" + field.Label).Disabled(s.Busy || s.Loading)
				})
			}
		},
	})
	if !s.PathDialog {
		a.cancelEnvironmentPaths()
	}
}
