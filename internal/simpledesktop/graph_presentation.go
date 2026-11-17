package simpledesktop

import (
	"strings"

	"github.com/egoist/mygo/ui"
)

var graphEmptyIcon = homeSVG(`<rect x="2" y="9" width="6" height="6" rx="1.5"/><rect x="16" y="2" width="6" height="6" rx="1.5"/><rect x="16" y="16" width="6" height="6" rx="1.5"/><path d="M8 12h4M12 5v14M12 5h4M12 19h4"/>`)

func (a *App) graphToolbar(c *ui.Context) {
	p, ok := a.current()
	code := a.graphMode == "代码结构"
	ui.Row(c).Shrink(0).Padding(controlGap, pageInset).Gap(12).AlignItems(ui.Center).Children(func() {
		selected := 0
		if code {
			selected = 1
		}
		ui.Segmented(c, &selected, "实体关系", "代码图谱").Label("图谱类型").OnChange(func() {
			a.graphMode = []string{"实体关系", "代码结构"}[selected]
			a.graph.clear()
			a.graphSelection, a.graphError = "", ""
			a.graphLoading = false
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
			textButton(c, "创建工具图谱", nil).OnClick(func() { a.previewToolGraph(p.ID) })
		}
		if code && ok && !p.CodeEnabled {
			return
		}
		refreshAction(c.Key("graph-refresh"), "刷新图谱", func() {
			a.loadGraph()
			a.restoreCodeState()
		}).Disabled(a.graphLoading)
		if code && ok {
			textButton(c, "关闭代码索引", nil).OnClick(func() { a.toggleCode(false) })
			ui.PrimaryButton(c, "更新代码图谱").Disabled(a.codeBusy || a.codeUnknown || a.codeRemoteActive).OnClick(func() { a.indexCode() })
		}
	})
	if !code && ok {
		a.graphPendingHint(c, p)
	}
}

func (a *App) codeGraphEmpty(c *ui.Context) {
	ui.Column(c).Grow(1).MinHeight(0).FillWidth().Padding(pageTopInset, pageInset, pageInset, pageInset).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
		ui.Column(c).MaxWidth(480).FillWidth().AlignItems(ui.Center).Gap(14).Children(func() {
			ui.Column(c).Size(88, 88).Radius(24).Background(ui.Hex("#f1f6fa")).AlignItems(ui.Center).Justify(ui.Center).Margin(0, 0, 8, 0).Children(func() {
				ui.Icon(c, graphEmptyIcon).FontSize(42).TextColor(ui.Hex("#91a9bf"))
			})
			homeText(c, "代码图谱尚未开启", homeHeading).FontSize(20).FontWeight(600).TextAlign(ui.Center)
			homeText(c, "从文件、函数与调用关系，了解这个项目的代码结构。", homeBody).TextAlign(ui.Center)
			homeText(c, "首次开启会建立代码索引，后续由你手动更新。", homeMeta).TextAlign(ui.Center)
			label := "开启代码图谱"
			if a.codeBusy {
				label = "正在开启…"
			}
			ui.PrimaryButton(c, label).Margin(10, 0, 0, 0).Disabled(a.codeBusy || a.codeUnknown || a.codeRemoteActive).OnClick(func() { a.toggleCode(true) })
			if a.graphError != "" {
				homeText(c, a.graphError, homeBody).MaxLines(4).TextAlign(ui.Center)
			}
			// Restored index activity is cached globally and is not scoped to this
			// disabled project. Do not present it as this project's enable result.
			if a.codeStatus != "" && !strings.HasPrefix(a.codeStatus, "官方代码索引状态：") {
				homeText(c, a.codeStatus, homeMeta).MaxLines(4).TextAlign(ui.Center)
			}
		})
	})
	appFooter(c, "图谱状态栏").Padding(0, pageInset).Children(func() {
		homeText(c, "仅当前项目 · 由你选择是否开启", homeMeta)
	})
}
