package simpledesktop

import (
	"strings"

	"github.com/egoist/mygo/ui"
)

func (a *App) graphToolbar(c *ui.Context) {
	p, ok := a.current()
	code := a.graphMode == "代码结构"
	ui.Row(c).Shrink(0).Padding(sp3, sp4, sp2, sp4).Gap(sp3).AlignItems(ui.Center).
		BorderWidth(0, 0, 1, 0).BorderColor(tok.Border).Children(func() {
		selected := 0
		if code {
			selected = 1
		}
		v4Segmented(c, "graph-mode-seg", "图谱类型", &selected, []string{"实体关系", "代码图谱"}, func() {
			a.resetGraphView([]string{"实体关系", "代码结构"}[selected])
			if selected == 1 && ok && !p.CodeEnabled {
				return
			}
			a.loadGraph()
		})
		readStatus := ""
		if a.graphLoading {
			readStatus = "正在读取…"
		}
		homeText(c.Key("graph-read-status"), readStatus, homeMeta).SingleLine()
		ui.Spacer(c.Key("graph-toolbar-spacer"))
		if !code && ok && a.Config.ProjectControl != nil {
			v4SmallButton(c, "创建工具图谱", nil, func() { a.previewToolGraph(p.ID) })
		}
		if code && ok && !p.CodeEnabled {
			return
		}
		if code && ok {
			v4SmallButton(c, "关闭代码索引", nil, func() { a.toggleCode(false) })
			v4SmallPrimaryButton(c, "更新代码图谱", func() { a.indexCode() }).Disabled(a.codeIndexBlocked())
		}
		v4SmallButton(c.Key("graph-refresh"), "刷新图谱", v4IconRefresh, func() {
			a.loadGraph()
			a.restoreCodeState()
		}).Disabled(a.graphLoading)
	})
	if !code && ok {
		a.graphPendingHint(c, p)
	}
}

func (a *App) codeGraphEmpty(c *ui.Context) {
	ui.Column(c).Grow(1).MinHeight(0).FillWidth().Background(tok.Bg).Padding(sp8).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
		ui.Column(c).MaxWidth(480).FillWidth().AlignItems(ui.Center).Gap(sp3).Children(func() {
			ui.Column(c).FillWidth().MaxHeight(200).AlignItems(ui.Center).Children(func() {
				v4EmptyState(c, "code-graph-empty", "代码图谱尚未开启", "从文件、函数与调用关系，了解这个项目的代码结构。首次开启会建立代码索引，后续由你手动更新。")
			})
			label := "开启代码图谱"
			if a.codeBusy {
				label = "正在开启…"
			}
			ui.PrimaryButton(c, label).Disabled(a.codeIndexBlocked()).OnClick(func() { a.toggleCode(true) })
			if a.graphError != "" {
				ui.Text(c, a.graphError).Font("system-ui").FontSize(fsFootnote).TextColor(tok.Danger).MaxLines(4).TextAlign(ui.Center)
			}
			// Restored index activity is cached globally and is not scoped to this
			// disabled project. Do not present it as this project's enable result.
			if a.codeStatus != "" && !strings.HasPrefix(a.codeStatus, "官方代码索引状态：") {
				homeText(c, a.codeStatus, homeMeta).MaxLines(4).TextAlign(ui.Center)
			}
		})
	})
	appFooter(c, "图谱状态栏").Padding(0, sp4).Children(func() {
		homeText(c, "仅当前项目 · 由你选择是否开启", homeMeta)
	})
}
