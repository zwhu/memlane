package simpledesktop

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/bootstrap"
)

// Path edits stay local to the dialog until the user explicitly requests detection.
type environmentPathDraft struct {
	Python, UV, Source, Archive, ModelCache, Plugin string
}

func (a *App) openEnvironmentPaths() {
	s := &a.environment
	if s.Busy || s.Loading || !s.Loaded {
		return
	}
	s.PathDraft = environmentPathDraft{s.Python, s.UV, s.Source, s.Archive, s.ModelCache, s.Plugin}
	s.PathDialog = true
}

func (a *App) cancelEnvironmentPaths() {
	a.environment.PathDialog = false
	a.environment.PathDraft = environmentPathDraft{}
}

func (a *App) detectEnvironmentPaths() {
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

func (a *App) environmentLocations(c *ui.Context) {
	s := &a.environment
	rows := environmentLocationRows(s.Plan.Items)
	if len(rows) == 0 {
		return
	}
	ui.Column(c.Key("environment-locations")).FillWidth().Gap(4).Padding(12, 0, 0, 0).BorderWidth(1, 0, 0, 0).BorderColor(homeBorder).Children(func() {
		ui.Row(c).FillWidth().MinHeight(24).AlignItems(ui.Center).Children(func() {
			homeText(c, "使用位置", homeMeta).FontWeight(500).TextColor(homeInk)
			ui.Spacer(c)
			if s.CopiedPath != "" {
				homeText(c, "路径已复制", homeMeta).TextColor(ui.Hex("#4d7a66"))
			}
		})
		for _, location := range rows {
			row := location
			line := ui.Row(c.Key("environment-location-" + row.Path)).FillWidth().MinWidth(0).MinHeight(26).AlignItems(ui.Center).Gap(12)
			line.Children(func() {
				homeText(c, row.Label, homeMeta).Width(132).Shrink(0).SingleLine().Tooltip(row.Label)
				homeText(c, environmentDisplayPath(row.Path), homeMeta).FontSize(12).TextColor(ui.Hex("#596675")).Grow(1).MinWidth(0).SingleLine().Tooltip(row.Path)
				hoverCopyAction(c, line, "复制"+row.Label+"位置", func() {
					c.WriteClipboard(row.Path)
					s.CopiedPath = row.Path
				})
			})
		}
	})
}

func (a *App) environmentPathsDialog(c *ui.Context) {
	s := &a.environment
	if !s.PathDialog {
		return
	}
	_, height := c.Size()
	modal := ui.Modal(c.Key("environment-path-dialog"), &s.PathDialog, func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
			homeText(c, "指定已有路径", homeHeading).FontSize(18)
			ui.Spacer(c)
			readingAction(c, "关闭路径编辑", readingClose, a.cancelEnvironmentPaths)
		})
		ui.Scroll(c.Key("environment-path-fields")).FillWidth().MaxHeight(height - 240).Children(func() {
			ui.Column(c).FillWidth().Gap(16).Children(func() {
				for _, field := range []struct {
					Label string
					Value *string
				}{{"Python 路径", &s.PathDraft.Python}, {"uv 路径", &s.PathDraft.UV}, {"Cognee 源码目录", &s.PathDraft.Source}, {"固定源码归档", &s.PathDraft.Archive}, {"已有模型缓存目录", &s.PathDraft.ModelCache}, {"已有 Agent 插件目录", &s.PathDraft.Plugin}} {
					ui.Column(c).FillWidth().Gap(4).Children(func() {
						homeText(c, field.Label, homeMeta).FontWeight(500)
						ui.TextInput(c.Key("environment-draft-"+field.Label), field.Value).FillWidth().Label("输入" + field.Label).Disabled(s.Busy || s.Loading)
					})
				}
			})
		})
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(12).Children(func() {
			ui.Spacer(c)
			ui.Button(c, "取消").OnClick(a.cancelEnvironmentPaths)
			ui.PrimaryButton(c, "检测这些路径").Disabled(s.Busy || s.Loading).OnClick(a.detectEnvironmentPaths)
		})
	}).Width(640).Padding(24).Gap(20).Transition(projectOverlayMotion())
	if modal.Dismissed() || !s.PathDialog {
		a.cancelEnvironmentPaths()
	}
}
